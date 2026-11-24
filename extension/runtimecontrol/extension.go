// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package runtimecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.uber.org/zap"

	configaws "github.com/aws/amazon-cloudwatch-agent/cfg/aws"
)

// Flags is what the extension offers to other components (found through
// component.Host.GetExtensions).
type Flags interface {
	// Flag returns the current value of the switch name. ok is false when the value is
	// unknown: no parameter configured, the parameter not read successfully yet, or the
	// key absent from it (or never a boolean). Safe for concurrent use; one atomic load.
	Flag(name string) (value bool, ok bool)
	// Register declares a switch a component uses: a value of the wrong type for it is
	// reported (W!) instead of silently ignored.
	Register(name string)
}

const (
	// supportedVersion of the parameter value's "version" key (optional).
	supportedVersion = 1
	// maxBackoffFactor caps the delay after consecutive failures at this many poll intervals.
	maxBackoffFactor = 4
	jitter           = 0.2
	requestTimeout   = 30 * time.Second
)

type ssmAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type clientFactory func(ctx context.Context, cfg *Config) (ssmAPI, error)

// Extension polls Config.Parameter and holds the last valid switch values.
type Extension struct {
	cfg       *Config
	logger    *zap.Logger
	id        string
	newClient clientFactory
	random    func() float64
	interval  time.Duration // effective poll interval

	flags atomic.Pointer[map[string]bool]

	regMu      sync.Mutex
	registered map[string]bool

	// Only used by the poll goroutine.
	client    ssmAPI
	lastValue string // value last applied (compared, not the version: a re-created parameter restarts at 1)
	loaded    bool
	failures  int

	cancel context.CancelFunc
	done   chan struct{}
}

var (
	_ extension.Extension = (*Extension)(nil)
	_ Flags               = (*Extension)(nil)
)

func newExtension(cfg *Config, logger *zap.Logger, id string, newClient clientFactory) *Extension {
	if newClient == nil {
		newClient = newSSMClient
	}
	e := &Extension{cfg: cfg, logger: logger, id: id, newClient: newClient, random: rand.Float64, registered: map[string]bool{}, interval: cfg.PollInterval}
	empty := map[string]bool{}
	e.flags.Store(&empty)
	return e
}

func newSSMClient(ctx context.Context, cfg *Config) (ssmAPI, error) {
	cc := &configaws.CredentialsConfig{Region: cfg.Region, Profile: cfg.Profile, Filename: cfg.SharedCredentialsFile}
	awsCfg, err := cc.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("AWS config: %w", err)
	}
	return ssm.NewFromConfig(awsCfg), nil
}

func (e *Extension) Flag(name string) (bool, bool) {
	v, ok := (*e.flags.Load())[name]
	return v, ok
}

func (e *Extension) Register(name string) {
	e.regMu.Lock()
	e.registered[name] = true
	e.regMu.Unlock()
}

func (e *Extension) isRegistered(name string) bool {
	e.regMu.Lock()
	defer e.regMu.Unlock()
	return e.registered[name]
}

func (e *Extension) Start(_ context.Context, _ component.Host) error {
	if e.cfg.Parameter == "" {
		e.logger.Info("runtimecontrol: no SSM parameter configured: disabled (no AWS calls); every switch uses its gate's default", zap.String("extension", e.id))
		return nil
	}
	interval, invalid, raised := e.cfg.problems()
	if invalid != nil {
		e.logger.Warn("runtimecontrol: disabled (no AWS calls); every switch uses its gate's default", zap.String("extension", e.id), zap.Error(invalid))
		return nil
	}
	if raised {
		e.logger.Warn("runtimecontrol: poll_interval too short, using the minimum", zap.Duration("poll_interval", e.interval), zap.Duration("minimum", interval))
	}
	e.interval = interval
	e.logger.Info("runtimecontrol: polling SSM parameter for runtime switches",
		zap.String("extension", e.id), zap.String("parameter", e.cfg.Parameter),
		zap.String("region", e.cfg.Region), zap.Duration("poll_interval", interval))
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel, e.done = cancel, make(chan struct{})
	go e.run(ctx)
	return nil
}

func (e *Extension) Shutdown(context.Context) error {
	if e.cancel != nil {
		e.cancel()
		<-e.done
	}
	return nil
}

func (e *Extension) run(ctx context.Context) {
	defer close(e.done)
	var delay time.Duration // first read right away
	for {
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		delay = e.poll(ctx)
	}
}

// poll reads the parameter once, applies a valid value, and returns the delay until the next read.
func (e *Extension) poll(ctx context.Context) time.Duration {
	if e.client == nil {
		c, err := e.newClient(ctx, e.cfg)
		if err != nil {
			return e.fail(err)
		}
		e.client = c
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	out, err := e.client.GetParameter(rctx, &ssm.GetParameterInput{Name: aws.String(e.cfg.Parameter), WithDecryption: aws.Bool(true)})
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return e.fail(err)
	}
	if out == nil || out.Parameter == nil || out.Parameter.Value == nil {
		return e.fail(errors.New("GetParameter returned no value"))
	}
	version, value := out.Parameter.Version, *out.Parameter.Value
	if !e.loaded || value != e.lastValue {
		next, err := e.parse(value)
		if err != nil {
			return e.fail(fmt.Errorf("parameter version %d: %w", version, err))
		}
		e.apply(next, version)
	}
	if e.failures > 0 {
		e.logger.Info("runtimecontrol: SSM parameter read again", zap.String("parameter", e.cfg.Parameter), zap.Int("failed_reads", e.failures))
	}
	e.failures = 0
	e.loaded, e.lastValue = true, value
	return e.jittered(e.interval)
}

// fail keeps the last values and backs off: poll_interval * 2^failures, at most maxBackoffFactor intervals.
func (e *Extension) fail(err error) time.Duration {
	e.failures++
	factor := min(1<<min(e.failures, 30), maxBackoffFactor)
	delay := e.jittered(time.Duration(factor) * e.interval)
	e.logger.Warn("runtimecontrol: could not read the runtime switches; keeping the last values",
		zap.String("parameter", e.cfg.Parameter), zap.Error(err),
		zap.String("current", e.describe(*e.flags.Load())), zap.Int("consecutive_failures", e.failures),
		zap.Duration("retry_in", delay.Round(time.Second)))
	return delay
}

func (e *Extension) jittered(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (1 - jitter + 2*jitter*e.random()))
}

// parse decodes the parameter value: a JSON object; "version" (optional) must be 1; every
// other key with a boolean value is a switch. Unknown keys with other values are ignored; a
// registered switch with a non-boolean value keeps its previous value (W!).
func (e *Extension) parse(value string) (map[string]bool, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(value))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if raw == nil {
		return nil, errors.New("invalid JSON object: null")
	}
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return nil, errors.New("invalid JSON object: trailing data")
	}
	if v, ok := raw["version"]; ok {
		var n any
		d := json.NewDecoder(bytes.NewReader(v))
		d.UseNumber()
		err := d.Decode(&n)
		num, isNum := n.(json.Number)
		if f, ferr := num.Float64(); err != nil || !isNum || ferr != nil || f != supportedVersion {
			return nil, fmt.Errorf("unsupported \"version\" %s (want %d)", v, supportedVersion)
		}
	}
	prev := *e.flags.Load()
	next := make(map[string]bool, len(raw))
	for k, v := range raw {
		if k == "version" {
			continue
		}
		var b bool
		if err := json.Unmarshal(v, &b); err != nil || string(bytes.TrimSpace(v)) == "null" {
			if e.isRegistered(k) {
				old, had := prev[k]
				e.logger.Warn("runtimecontrol: switch value is not true/false; keeping its previous value",
					zap.String("parameter", e.cfg.Parameter), zap.String("switch", k), zap.ByteString("value", v),
					zap.String("previous", describeOne(old, had)))
				if had {
					next[k] = old
				}
			}
			continue
		}
		next[k] = b
	}
	return next, nil
}

func (e *Extension) apply(next map[string]bool, version int64) {
	prev := *e.flags.Load()
	e.flags.Store(&next)
	first := !e.loaded
	names := make([]string, 0, len(prev)+len(next))
	for k := range prev {
		names = append(names, k)
	}
	for k := range next {
		if _, ok := prev[k]; !ok {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	for _, k := range names {
		o, hadO := prev[k]
		n, hasN := next[k]
		if hadO == hasN && o == n {
			continue
		}
		e.logger.Info("runtimecontrol: switch changed", zap.String("switch", k),
			zap.String("from", describeOne(o, hadO)), zap.String("to", describeOne(n, hasN)),
			zap.String("parameter", e.cfg.Parameter), zap.Int64("parameter_version", version))
	}
	if first {
		e.logger.Info("runtimecontrol: runtime switches loaded", zap.String("parameter", e.cfg.Parameter),
			zap.Int64("parameter_version", version), zap.String("switches", e.describe(next)))
	}
}

func describeOne(v, ok bool) string {
	if !ok {
		return "unset (gate default)"
	}
	return fmt.Sprint(v)
}

func (e *Extension) describe(m map[string]bool) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, m[k])
	}
	return strings.Join(parts, ",")
}
