// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package obiconfigtest checks the eBPF process's default config (ebpf/obi.yaml) with
// OBI's own loader. It is a separate module, like the eBPF binary: the agent's module
// does not depend on OBI.
package obiconfigtest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/config"
	"go.opentelemetry.io/obi/pkg/export/otel/otelcfg"
	"go.opentelemetry.io/obi/pkg/obi"
)

const obiConfig = "../../packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml"

func load(t *testing.T) *obi.Config {
	f, err := os.Open(obiConfig)
	require.NoError(t, err)
	defer f.Close()
	cfg, err := obi.LoadConfig(f)
	require.NoError(t, err)
	return cfg
}

func TestOBIConfigValidates(t *testing.T) {
	require.NoError(t, load(t).Validate())
}

// RED metrics only, over OTLP/gRPC to the agent's Unix socket; no traces, no other
// exporter, no network flows. The process holds no AWS credentials: nothing here
// points at AWS.
func TestOBIConfigExportsMetricsToTheAgentSocket(t *testing.T) {
	cfg := load(t)
	assert.Equal(t, "unix:///opt/aws/amazon-cloudwatch-agent/var/ebpf/otlp.sock", cfg.OTELMetrics.MetricsEndpoint)
	assert.Equal(t, otelcfg.ProtocolGRPC, cfg.OTELMetrics.Protocol)
	assert.False(t, cfg.Traces.Enabled(), "traces export")
	assert.False(t, cfg.Prometheus.EndpointEnabled(), "prometheus export")
	assert.False(t, cfg.NetworkFlows.Enable, "network flows")
	assert.Equal(t, config.ContextPropagationTCPServiceName, cfg.EBPF.ContextPropagation)
	assert.False(t, cfg.EnforceSysCaps)
}

// Defaults chosen from the EC2 performance tests (perf-ec2, 2026-10-22/23): BPF maps at half
// size (memory ~330 MB -> ~170 MB, RED exact up to ~4k requests in flight) and no Node.js
// injector (RED does not need it; it cost +340 us CPU per request). SDK-instrumented
// processes keep their eBPF RED (upstream default true drops them).
func TestOBIConfigPerformanceDefaults(t *testing.T) {
	cfg := load(t)
	assert.Equal(t, -1, cfg.EBPF.MapsConfig.GlobalScaleFactor)
	assert.False(t, cfg.NodeJS.Enabled)
	assert.False(t, cfg.Discovery.ExcludeOTelInstrumentedServices)
}
