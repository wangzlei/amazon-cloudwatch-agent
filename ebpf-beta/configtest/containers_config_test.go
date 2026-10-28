// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package configtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/otelcol"
	"go.uber.org/zap"

	"github.com/aws/amazon-cloudwatch-agent/service/configprovider"
	"github.com/aws/amazon-cloudwatch-agent/service/defaultcomponents"
)

// privilegedConfig is the config of the privileged eBPF container (EKS / ECS).
const privilegedConfig = "../containers/privileged.yaml"

func resolve(t *testing.T, path string) map[string]any {
	r, err := confmap.NewResolver(configprovider.GetSettings([]string{path}, zap.NewNop()).ResolverSettings)
	require.NoError(t, err)
	conf, err := r.Resolve(context.Background())
	require.NoError(t, err)
	return conf.ToStringMap()
}

func TestPrivilegedConfigValidates(t *testing.T) {
	t.Setenv("CWAGENT_OTLP_ENDPOINT", "http://127.0.0.1:4328")
	col, err := otelcol.NewCollector(otelcol.CollectorSettings{
		BuildInfo:              component.NewDefaultBuildInfo(),
		Factories:              defaultcomponents.Factories,
		ConfigProviderSettings: configprovider.GetSettings([]string{privilegedConfig}, zap.NewNop()),
	})
	require.NoError(t, err)
	require.NoError(t, col.DryRun(context.Background()))
}

// The privileged container holds no AWS credentials: no extensions (no sigv4auth), and
// its only exporter is plain OTLP/HTTP to the regular agent.
func TestPrivilegedConfigExportsOnlyToTheAgent(t *testing.T) {
	t.Setenv("CWAGENT_OTLP_ENDPOINT", "http://127.0.0.1:4328")
	conf := resolve(t, privilegedConfig)
	assert.NotContains(t, conf, "extensions")
	exporters := conf["exporters"].(map[string]any)
	require.Len(t, exporters, 1)
	exp := exporters["otlp_http/cwagent_ebpf_agent"].(map[string]any)
	assert.Equal(t, "http://127.0.0.1:4328", exp["endpoint"])
	assert.NotContains(t, exp, "auth")
	svc := conf["service"].(map[string]any)
	assert.NotContains(t, svc, "extensions")
	pipelines := svc["pipelines"].(map[string]any)
	require.Len(t, pipelines, 1)
	assert.Equal(t, []any{"otlp_http/cwagent_ebpf_agent"}, pipelines["metrics/cwagent_ebpf"].(map[string]any)["exporters"])
}

// The receiver and processors are copies of the RPM's etc/ebpf/obi.yaml: keep them equal.
func TestPrivilegedConfigMatchesRPMConfig(t *testing.T) {
	setCreds(t)
	t.Setenv("CWAGENT_OTLP_ENDPOINT", "http://127.0.0.1:4328")
	priv, rpm := resolve(t, privilegedConfig), resolve(t, rpmConfig)
	assert.Equal(t, rpm["receivers"], priv["receivers"])
	assert.Equal(t, rpm["processors"], priv["processors"])
	rpmPipe := rpm["service"].(map[string]any)["pipelines"].(map[string]any)["metrics/cwagent_ebpf"].(map[string]any)
	privPipe := priv["service"].(map[string]any)["pipelines"].(map[string]any)["metrics/cwagent_ebpf"].(map[string]any)
	assert.Equal(t, rpmPipe["processors"], privPipe["processors"])
}
