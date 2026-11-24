// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package runtimecontrol

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type response struct {
	value   string
	version int64
	err     error
}

type fakeSSM struct {
	mu    sync.Mutex
	calls int
	names []string
	next  response
}

func (f *fakeSSM) set(r response) { f.mu.Lock(); f.next = r; f.mu.Unlock() }

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.names = append(f.names, aws.ToString(in.Name))
	if f.next.err != nil {
		return nil, f.next.err
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(f.next.value), Version: f.next.version}}, nil
}

func (f *fakeSSM) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func newTest(t *testing.T, fake *fakeSSM) (*Extension, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	cfg := &Config{Parameter: "AmazonCloudWatch-agent-runtime", PollInterval: 100 * time.Second, Region: "us-west-2"}
	e := newExtension(cfg, zap.New(core), "runtimecontrol/test", func(context.Context, *Config) (ssmAPI, error) { return fake, nil })
	e.random = func() float64 { return 0.5 } // no jitter
	return e, logs
}

func flag(t *testing.T, e *Extension, name string) any {
	t.Helper()
	v, ok := e.Flag(name)
	if !ok {
		return nil
	}
	return v
}

func messages(logs *observer.ObservedLogs, level string) []string {
	var out []string
	for _, l := range logs.All() {
		if l.Level.String() == level {
			out = append(out, l.Message)
		}
	}
	return out
}

func TestTransitions(t *testing.T) {
	fake := &fakeSSM{}
	e, logs := newTest(t, fake)
	e.Register("traces_export")
	e.Register("emf_export")
	ctx := context.Background()

	assert.Nil(t, flag(t, e, "traces_export"), "unknown before the first read")

	fake.set(response{value: `{"version":1,"traces_export":false,"emf_export":false}`, version: 1})
	assert.Equal(t, 100*time.Second, e.poll(ctx))
	assert.Equal(t, false, flag(t, e, "traces_export"))
	assert.Equal(t, false, flag(t, e, "emf_export"))
	assert.Equal(t, []string{"AmazonCloudWatch-agent-runtime"}, fake.names)

	logs.TakeAll()
	fake.set(response{value: `{"version":1,"traces_export":true,"emf_export":false,"future_key":{"x":1}}`, version: 2})
	e.poll(ctx)
	assert.Equal(t, true, flag(t, e, "traces_export"))
	assert.Equal(t, false, flag(t, e, "emf_export"))
	assert.Nil(t, flag(t, e, "future_key"), "unknown non-boolean key ignored")
	changed := logs.FilterMessage("runtimecontrol: switch changed").All()
	require.Len(t, changed, 1, "one I! line per transition")
	assert.Equal(t, "traces_export", changed[0].ContextMap()["switch"])
	assert.Equal(t, "false", changed[0].ContextMap()["from"])
	assert.Equal(t, "true", changed[0].ContextMap()["to"])
	assert.Empty(t, messages(logs, "warn"))

	// same value again: no change, no log
	logs.TakeAll()
	e.poll(ctx)
	assert.Empty(t, logs.FilterMessage("runtimecontrol: switch changed").All())

	// key removed: back to unknown (gate default)
	fake.set(response{value: `{"version":1,"emf_export":true}`, version: 3})
	e.poll(ctx)
	assert.Nil(t, flag(t, e, "traces_export"))
	assert.Equal(t, true, flag(t, e, "emf_export"))
	assert.Len(t, logs.FilterMessage("runtimecontrol: switch changed").All(), 2)

	// version is optional
	fake.set(response{value: `{"traces_export":true}`, version: 4})
	e.poll(ctx)
	assert.Equal(t, true, flag(t, e, "traces_export"))
}

func TestInvalidValueKeepsLast(t *testing.T) {
	fake := &fakeSSM{}
	e, logs := newTest(t, fake)
	e.Register("traces_export")
	e.Register("emf_export")
	ctx := context.Background()
	fake.set(response{value: `{"version":1,"traces_export":true,"emf_export":false}`, version: 1})
	e.poll(ctx)

	for _, bad := range []string{`{"version":1,"traces_export":fals`, `not json`, `[true]`, `null`, `{"version":2,"traces_export":false}`, `{"version":"1","traces_export":false}`, `{} {}`} {
		logs.TakeAll()
		fake.set(response{value: bad, version: 2})
		e.poll(ctx)
		assert.Equal(t, true, flag(t, e, "traces_export"), bad)
		assert.Equal(t, false, flag(t, e, "emf_export"), bad)
		assert.Equal(t, []string{"runtimecontrol: could not read the runtime switches; keeping the last values"}, messages(logs, "warn"), bad)
		e.failures = 0
	}

	// wrong type for a registered switch: that switch keeps its value, the others apply
	logs.TakeAll()
	fake.set(response{value: `{"version":1,"traces_export":"false","emf_export":true}`, version: 3})
	e.poll(ctx)
	assert.Equal(t, true, flag(t, e, "traces_export"))
	assert.Equal(t, true, flag(t, e, "emf_export"))
	assert.Equal(t, []string{"runtimecontrol: switch value is not true/false; keeping its previous value"}, messages(logs, "warn"))

	// valid again
	fake.set(response{value: `{"version":1,"traces_export":false,"emf_export":false}`, version: 4})
	e.poll(ctx)
	assert.Equal(t, false, flag(t, e, "traces_export"))
}

func TestErrorBackoff(t *testing.T) {
	fake := &fakeSSM{}
	e, logs := newTest(t, fake)
	ctx := context.Background()
	fake.set(response{value: `{"traces_export":true}`, version: 1})
	assert.Equal(t, 100*time.Second, e.poll(ctx))

	fake.set(response{err: &types.ParameterNotFound{Message: aws.String("deleted")}})
	assert.Equal(t, 200*time.Second, e.poll(ctx))
	fake.set(response{err: errors.New("ThrottlingException: Rate exceeded")})
	assert.Equal(t, 400*time.Second, e.poll(ctx))
	assert.Equal(t, 400*time.Second, e.poll(ctx), "capped at 4 intervals")
	assert.Equal(t, 400*time.Second, e.poll(ctx))
	assert.Equal(t, true, flag(t, e, "traces_export"), "last value kept")
	assert.Len(t, messages(logs, "warn"), 4)

	// a re-created parameter starts at version 1 again: the value is compared, not the version
	fake.set(response{value: `{"traces_export":false}`, version: 1})
	assert.Equal(t, 100*time.Second, e.poll(ctx))
	assert.Equal(t, false, flag(t, e, "traces_export"))
	assert.NotEmpty(t, logs.FilterMessage("runtimecontrol: SSM parameter read again").All())

	// client creation errors back off too
	e2, _ := newTest(t, fake)
	e2.newClient = func(context.Context, *Config) (ssmAPI, error) { return nil, errors.New("no credentials") }
	assert.Equal(t, 200*time.Second, e2.poll(ctx))
}

func TestJitter(t *testing.T) {
	e, _ := newTest(t, &fakeSSM{})
	e.random = func() float64 { return 0 }
	assert.Equal(t, 80*time.Second, e.jittered(100*time.Second))
	e.random = func() float64 { return 0.999999 }
	assert.InDelta(t, float64(120*time.Second), float64(e.jittered(100*time.Second)), float64(time.Millisecond))
}

func TestDisabledMakesNoCalls(t *testing.T) {
	created := false
	core, logs := observer.New(zap.InfoLevel)
	e := newExtension(&Config{PollInterval: defaultPollInterval}, zap.New(core), "runtimecontrol/test", func(context.Context, *Config) (ssmAPI, error) {
		created = true
		return &fakeSSM{}, nil
	})
	require.NoError(t, e.Start(context.Background(), componenttest.NewNopHost()))
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, e.Shutdown(context.Background()))
	assert.False(t, created, "no AWS client when no parameter is configured")
	_, ok := e.Flag("traces_export")
	assert.False(t, ok)
	assert.Len(t, logs.FilterMessageSnippet("disabled").All(), 1)
}

func TestStartPollsAndShutdownStops(t *testing.T) {
	fake := &fakeSSM{}
	fake.set(response{value: `{"version":1,"traces_export":true}`, version: 1})
	e, _ := newTest(t, fake)
	require.NoError(t, e.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { v, ok := e.Flag("traces_export"); return ok && v }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, e.Shutdown(context.Background()))
	n := fake.count()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, n, fake.count())
	assert.Equal(t, 1, n, "next read only after poll_interval")
}

func TestConfig(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	assert.Equal(t, 300*time.Second, cfg.PollInterval)
	assert.Equal(t, "", cfg.Parameter)
	assert.NoError(t, cfg.Validate())

	require.NoError(t, confmap.NewFromStringMap(map[string]any{"parameter": "AmazonCloudWatch-agent-runtime", "poll_interval": "30s", "region": "us-west-2"}).Unmarshal(cfg))
	assert.Equal(t, 30*time.Second, cfg.PollInterval)
	assert.NoError(t, cfg.Validate())

	// never fatal for the agent: an invalid name disables the extension, a short interval is raised
	for _, c := range []*Config{{Parameter: "a b", PollInterval: time.Minute}, {Parameter: "ok", PollInterval: time.Second}} {
		assert.NoError(t, c.Validate())
	}
	_, invalid, _ := (&Config{Parameter: "a b", PollInterval: time.Minute}).problems()
	assert.Error(t, invalid)
	iv, invalid, raised := (&Config{Parameter: "ok", PollInterval: time.Second}).problems()
	assert.NoError(t, invalid)
	assert.True(t, raised)
	assert.Equal(t, 10*time.Second, iv)
	for _, ok := range []string{"arn:aws:ssm:us-west-2:123456789012:parameter/x", "/a/b-c_d.e", "AmazonCloudWatch-agent-runtime"} {
		_, invalid, _ = (&Config{Parameter: ok, PollInterval: time.Minute}).problems()
		assert.NoError(t, invalid, ok)
	}

	// invalid name: Start makes no client
	created := false
	e := newExtension(&Config{Parameter: "bad name", PollInterval: time.Minute}, zap.NewNop(), "x", func(context.Context, *Config) (ssmAPI, error) { created = true; return &fakeSSM{}, nil })
	require.NoError(t, e.Start(context.Background(), componenttest.NewNopHost()))
	require.NoError(t, e.Shutdown(context.Background()))
	assert.False(t, created)

	ext, err := f.Create(context.Background(), extensiontest.NewNopSettings(TypeStr), f.CreateDefaultConfig())
	require.NoError(t, err)
	_, ok := ext.(Flags)
	assert.True(t, ok)
}
