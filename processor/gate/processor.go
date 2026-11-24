// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package gate

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.uber.org/zap"

	"github.com/aws/amazon-cloudwatch-agent/extension/runtimecontrol"
)

const (
	stateUnknown int32 = iota
	stateOff
	stateOn
)

type gate struct {
	cfg    *Config
	logger *zap.Logger
	id     string
	flags  runtimecontrol.Flags // nil: no extension, always Default
	last   atomic.Int32         // last state logged
}

func newGate(cfg *Config, set processor.Settings) *gate {
	return &gate{cfg: cfg, logger: set.Logger, id: set.ID.String()}
}

func (g *gate) start(_ context.Context, host component.Host) error {
	flags, err := findFlags(host, g.cfg.Extension)
	if err != nil {
		return fmt.Errorf("gate %s: %w", g.id, err)
	}
	g.flags = flags
	if flags == nil {
		g.logger.Info("gate: no runtimecontrol extension; the switch keeps its default",
			zap.String("gate", g.id), zap.String("switch", g.cfg.Flag), zap.Bool("default", g.cfg.Default))
		return nil
	}
	flags.Register(g.cfg.Flag)
	return nil
}

func findFlags(host component.Host, want *component.ID) (runtimecontrol.Flags, error) {
	exts := host.GetExtensions()
	if want != nil {
		c, ok := exts[*want]
		if !ok {
			return nil, fmt.Errorf("extension %s is not in service::extensions", want)
		}
		f, ok := c.(runtimecontrol.Flags)
		if !ok {
			return nil, fmt.Errorf("extension %s is not a runtimecontrol extension", want)
		}
		return f, nil
	}
	var ids []component.ID
	for id, c := range exts {
		if _, ok := c.(runtimecontrol.Flags); ok {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return nil, nil
	case 1:
		return exts[ids[0]].(runtimecontrol.Flags), nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return nil, fmt.Errorf("several runtimecontrol extensions (%v): set extension", ids)
}

// open reports whether data passes: the switch's value, else Default. One atomic load (+ map
// lookup) per batch; a change of the effective state is logged once.
func (g *gate) open() bool {
	on, ok := false, false
	if g.flags != nil {
		on, ok = g.flags.Flag(g.cfg.Flag)
	}
	if !ok {
		on = g.cfg.Default
	}
	s := stateOff
	if on {
		s = stateOn
	}
	if prev := g.last.Swap(s); prev != s {
		g.logger.Info("gate: data "+map[bool]string{true: "passes", false: "is dropped"}[on],
			zap.String("gate", g.id), zap.String("switch", g.cfg.Flag), zap.Bool("value", on), zap.Bool("from_default", !ok))
	}
	return on
}

func (g *gate) processTraces(_ context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	if !g.open() {
		return td, processorhelper.ErrSkipProcessingData
	}
	return td, nil
}

func (g *gate) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	if !g.open() {
		return md, processorhelper.ErrSkipProcessingData
	}
	return md, nil
}

func (g *gate) processLogs(_ context.Context, ld plog.Logs) (plog.Logs, error) {
	if !g.open() {
		return ld, processorhelper.ErrSkipProcessingData
	}
	return ld, nil
}
