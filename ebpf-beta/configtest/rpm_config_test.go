// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package configtest checks that the agent's eBPF receiving pipeline shipped in the RPM
// (etc/ebpf/receiver.yaml) loads and validates with the components compiled into
// this agent, through the agent's own config provider (service/configprovider,
// including its extra env expansion), like `otelcol validate`. The eBPF process's
// own config (ebpf/obi.yaml) is checked by ebpf-beta/obiconfigtest.
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

const rpmConfig = "../../packaging/dependencies/amazon-cloudwatch-agent-ebpf-receiver.yaml"

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

// The only receiver is OTLP/gRPC on the eBPF Unix socket, the only pipeline sends metrics
// to CloudWatch (no traces, no debug exporter).
func TestRPMDefaultEBPFConfigReceivesOnTheSocketAndExportsMetricsOnly(t *testing.T) {
	setCreds(t)
	r, err := confmap.NewResolver(configprovider.GetSettings([]string{rpmConfig}, zap.NewNop()).ResolverSettings)
	require.NoError(t, err)
	conf, err := r.Resolve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"otlp/cwagent_ebpf"}, keys(conf.Get("receivers").(map[string]any)))
	assert.Equal(t, "/opt/aws/amazon-cloudwatch-agent/var/ebpf/otlp.sock", conf.Get("receivers::otlp/cwagent_ebpf::protocols::grpc::endpoint"))
	assert.Equal(t, "unix", conf.Get("receivers::otlp/cwagent_ebpf::protocols::grpc::transport"))
	assert.Nil(t, conf.Get("receivers::otlp/cwagent_ebpf::protocols::http"))
	pipelines := conf.Get("service::pipelines").(map[string]any)
	assert.Equal(t, []string{"metrics/cwagent_ebpf"}, keys(pipelines))
	assert.Equal(t, []string{"otlp_http/cwagent_ebpf_cloudwatch"}, keys(conf.Get("exporters").(map[string]any)))
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
			assert.Equal(t, "https://monitoring."+tc.want+".amazonaws.com/v1/metrics", conf.Get("exporters::otlp_http/cwagent_ebpf_cloudwatch::metrics_endpoint"))
			assert.Equal(t, tc.want, conf.Get("extensions::sigv4auth/cwagent_ebpf_monitoring::region"))
		})
	}
}

// A user YAML (etc/amazon-cloudwatch-agent.d/ or CW_OTEL_CONFIG_CONTENT) with the usual
// component and pipeline names must merge with the eBPF receiving pipeline: the agent rejects
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
	assert.Contains(t, pipelines, "metrics/cwagent_ebpf")
	assert.Contains(t, pipelines, "metrics")
	assert.Contains(t, pipelines, "traces")
}
