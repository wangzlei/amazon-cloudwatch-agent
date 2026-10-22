// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || arm64)

package obireceiver // import "github.com/aws/amazon-cloudwatch-agent/receiver/obireceiver"

import (
	"context"
	"errors"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver"

	"go.opentelemetry.io/obi/collector"
	"go.opentelemetry.io/obi/pkg/obi"
)

// Config is the receiver configuration. Its YAML schema is OBI's standard
// configuration file schema (discovery, ebpf, attributes, routes, ...; see
// https://opentelemetry.io/docs/zero-code/obi/configure/), decoded on top of
// the CloudWatch Agent defaults returned by DefaultOBIConfig.
type Config struct {
	runtime *obi.Config
}

var (
	_ confmap.Unmarshaler = (*Config)(nil)
	_ component.Config    = (*Config)(nil)
)

// OBI returns the underlying OBI runtime configuration.
func (c *Config) OBI() *obi.Config { return c.runtime }

// Unmarshal decodes the receiver's YAML block on top of the defaults.
func (c *Config) Unmarshal(conf *confmap.Conf) error {
	if c.runtime == nil {
		c.runtime = DefaultOBIConfig()
	}
	return c.runtime.Unmarshal(conf)
}

// Validate validates the OBI configuration in receiver mode: traces and
// metrics are delivered to the collector pipeline, so no OTLP/Prometheus
// exporter endpoint has to be configured inside OBI.
func (c *Config) Validate() error {
	if c == nil || c.runtime == nil {
		return errors.New("obi receiver: empty configuration")
	}
	return c.runtime.ValidateForReceiver()
}

func createDefaultConfig() component.Config {
	return &Config{runtime: DefaultOBIConfig()}
}

func runtimeOf(cfg component.Config) (*obi.Config, error) {
	c, ok := cfg.(*Config)
	if !ok || c.runtime == nil {
		return nil, errors.New("obi receiver: invalid configuration type")
	}
	// The traces and metrics receivers created from the same component ID share
	// one OBI instance (OBI's controller); a signal without a pipeline gets a
	// no-op consumer, exactly as OBI's own receiver config does.
	if c.runtime.Traces.TracesConsumer == nil {
		c.runtime.Traces.TracesConsumer = consumertest.NewNop()
	}
	if c.runtime.OTELMetrics.MetricsConsumer == nil {
		c.runtime.OTELMetrics.MetricsConsumer = consumertest.NewNop()
	}
	return c.runtime, nil
}

func createTraces(ctx context.Context, set receiver.Settings, cfg component.Config, next consumer.Traces) (receiver.Traces, error) {
	rc, err := runtimeOf(cfg)
	if err != nil {
		return nil, err
	}
	return collector.BuildTracesReceiver()(ctx, set, rc, next)
}

func createMetrics(ctx context.Context, set receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
	rc, err := runtimeOf(cfg)
	if err != nil {
		return nil, err
	}
	return collector.BuildMetricsReceiver()(ctx, set, rc, next)
}
