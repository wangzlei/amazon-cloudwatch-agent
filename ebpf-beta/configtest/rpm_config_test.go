// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package configtest checks that the eBPF default config shipped in the RPM
// (etc/ebpf/obi.yaml) loads and validates with the components compiled into
// this agent, through the agent's own config provider (service/configprovider,
// including its extra env expansion), like `otelcol validate`.
package configtest

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/otelcol"
	"go.uber.org/zap"

	mergeconfmap "github.com/aws/amazon-cloudwatch-agent/internal/merge/confmap"
	"github.com/aws/amazon-cloudwatch-agent/service/configprovider"
	"github.com/aws/amazon-cloudwatch-agent/service/defaultcomponents"
)

const rpmConfig = "../../packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml"

func setCreds(t *testing.T) {
	// sigv4auth resolves a credential provider during validation.
	t.Setenv("AWS_ACCESS_KEY_ID", "ci")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ci")
}

// setOrUnset sets key, or unsets it for "" (env-config.json omits CWAGENT_REGION when
// there is no region; an empty but set variable would not take the YAML default).
func setOrUnset(t *testing.T, key, value string) {
	t.Setenv(key, value) // restores the original value after the test
	if value == "" {
		require.NoError(t, os.Unsetenv(key))
	}
}

func TestRPMDefaultEBPFConfigValidates(t *testing.T) {
	setCreds(t)
	col, err := otelcol.NewCollector(otelcol.CollectorSettings{
		BuildInfo:              component.NewDefaultBuildInfo(),
		Factories:              defaultcomponents.Factories,
		ConfigProviderSettings: configprovider.GetSettings([]string{rpmConfig}, zap.NewNop()),
	})
	require.NoError(t, err)
	require.NoError(t, col.DryRun(context.Background()))
}

// Traces are off by default: every discovery instrument entry exports metrics only, so
// OBI builds no spans and the traces pipeline stays idle until exports includes traces.
func TestRPMDefaultEBPFConfigExportsMetricsOnly(t *testing.T) {
	setCreds(t)
	r, err := confmap.NewResolver(configprovider.GetSettings([]string{rpmConfig}, zap.NewNop()).ResolverSettings)
	require.NoError(t, err)
	conf, err := r.Resolve(context.Background())
	require.NoError(t, err)
	for _, e := range conf.Get("receivers::obi/cwagent_ebpf::discovery::instrument").([]any) {
		assert.Equal(t, "metrics", e.(map[string]any)["exports"], "instrument entry %v", e)
	}
}

// Defaults chosen from the EC2 performance tests (perf-ec2, 2026-10-22/23): BPF maps at half
// size (agent memory ~330 MB -> ~170 MB, RED exact up to ~4k requests in flight) and no
// Node.js injector (RED does not need it; it cost +340 us CPU per request).
func TestRPMDefaultEBPFConfigPerformanceDefaults(t *testing.T) {
	setCreds(t)
	r, err := confmap.NewResolver(configprovider.GetSettings([]string{rpmConfig}, zap.NewNop()).ResolverSettings)
	require.NoError(t, err)
	conf, err := r.Resolve(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, -1, conf.Get("receivers::obi/cwagent_ebpf::ebpf::maps_config::global_scale_factor"))
	assert.Equal(t, false, conf.Get("receivers::obi/cwagent_ebpf::nodejs::enabled"))
}

// The exporters and sigv4auth use CWAGENT_REGION (the region the agent resolved, written to
// env-config.json by the translation), else us-west-2. AWS_REGION alone does not change it:
// the translation folds it into CWAGENT_REGION when it is the agent's region.
func TestRPMDefaultEBPFConfigRegion(t *testing.T) {
	for _, tc := range []struct {
		name, cwagentRegion, awsRegion, want string
	}{
		{"fallback", "", "", "us-west-2"},
		{"AWS_REGION alone", "", "eu-west-1", "us-west-2"},
		{"CWAGENT_REGION", "ap-south-1", "eu-west-1", "ap-south-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setCreds(t)
			setOrUnset(t, "CWAGENT_REGION", tc.cwagentRegion)
			setOrUnset(t, "AWS_REGION", tc.awsRegion)
			r, err := confmap.NewResolver(configprovider.GetSettings([]string{rpmConfig}, zap.NewNop()).ResolverSettings)
			require.NoError(t, err)
			conf, err := r.Resolve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "https://xray."+tc.want+".amazonaws.com/v1/traces", conf.Get("exporters::otlp_http/cwagent_ebpf_xray::traces_endpoint"))
			assert.Equal(t, "https://monitoring."+tc.want+".amazonaws.com/v1/metrics", conf.Get("exporters::otlp_http/cwagent_ebpf_cloudwatch::metrics_endpoint"))
			assert.Equal(t, tc.want, conf.Get("extensions::sigv4auth/cwagent_ebpf_xray::region"))
			assert.Equal(t, tc.want, conf.Get("extensions::sigv4auth/cwagent_ebpf_monitoring::region"))
		})
	}
}

// A user YAML (etc/amazon-cloudwatch-agent.d/ or CW_OTEL_CONFIG_CONTENT) with the usual
// component and pipeline names must merge with the eBPF default config: the agent rejects
// duplicate keys that differ (internal/merge/confmap), so every name there is
// <type>/cwagent_ebpf...
func TestRPMDefaultEBPFConfigMergesWithUserYAML(t *testing.T) {
	user := []byte(`
receivers:
  obi: {}
  otlp: {protocols: {grpc: {}}}
extensions:
  sigv4auth: {region: us-east-1}
  sigv4auth/xray: {region: us-east-1, service: xray}
processors:
  batch: {}
  cumulativetodelta: {}
  transform/aws: {}
exporters:
  debug: {verbosity: detailed}
  otlp_http/xray: {traces_endpoint: https://example.com}
  otlp_http/cloudwatch: {metrics_endpoint: https://example.com}
service:
  extensions: [sigv4auth]
  pipelines:
    traces: {receivers: [otlp], processors: [batch], exporters: [debug]}
    metrics: {receivers: [otlp], processors: [batch], exporters: [debug]}
`)
	result := mergeconfmap.New()
	for _, l := range []mergeconfmap.Loader{mergeconfmap.NewFileLoader(rpmConfig), mergeconfmap.NewByteLoader("user", user)} {
		conf, err := l.Load()
		require.NoError(t, err)
		require.NoError(t, result.Merge(conf))
	}
	pipelines := result.ToStringMap()["service"].(map[string]any)["pipelines"].(map[string]any)
	assert.Contains(t, pipelines, "traces/cwagent_ebpf")
	assert.Contains(t, pipelines, "traces")
}
