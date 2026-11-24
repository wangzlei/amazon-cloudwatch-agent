// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package gate is a processor that passes or drops all data of its pipeline according to a
// runtime switch of the runtimecontrol extension (read once per batch, no restart). Put it
// first in the pipeline so that no work is done for data that is dropped.
package gate

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

var TypeStr = component.MustNewType("gate")

const stability = component.StabilityLevelAlpha

// The gate never modifies data: a read-only first consumer lets the fan-out skip a copy.
var capabilities = consumer.Capabilities{MutatesData: false}

func NewFactory() processor.Factory {
	return processor.NewFactory(
		TypeStr,
		createDefaultConfig,
		processor.WithTraces(createTraces, stability),
		processor.WithMetrics(createMetrics, stability),
		processor.WithLogs(createLogs, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createTraces(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	g := newGate(cfg.(*Config), set)
	return processorhelper.NewTraces(ctx, set, cfg, next, g.processTraces,
		processorhelper.WithStart(g.start), processorhelper.WithCapabilities(capabilities))
}

func createMetrics(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	g := newGate(cfg.(*Config), set)
	return processorhelper.NewMetrics(ctx, set, cfg, next, g.processMetrics,
		processorhelper.WithStart(g.start), processorhelper.WithCapabilities(capabilities))
}

func createLogs(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	g := newGate(cfg.(*Config), set)
	return processorhelper.NewLogs(ctx, set, cfg, next, g.processLogs,
		processorhelper.WithStart(g.start), processorhelper.WithCapabilities(capabilities))
}
