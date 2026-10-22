// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package configtest checks that the eBPF default config shipped in the RPM
// (etc/ebpf/obi.yaml) loads and validates with the components compiled into
// this agent, like `otelcol validate`.
package configtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/provider/envprovider"
	"go.opentelemetry.io/collector/confmap/provider/fileprovider"
	"go.opentelemetry.io/collector/otelcol"

	"github.com/aws/amazon-cloudwatch-agent/service/defaultcomponents"
)

const rpmConfig = "../../packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml"

func TestRPMDefaultEBPFConfigValidates(t *testing.T) {
	// sigv4auth resolves a credential provider during validation.
	t.Setenv("AWS_ACCESS_KEY_ID", "ci")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ci")
	t.Setenv("AWS_REGION", "us-west-2")
	col, err := otelcol.NewCollector(otelcol.CollectorSettings{
		BuildInfo: component.NewDefaultBuildInfo(),
		Factories: defaultcomponents.Factories,
		ConfigProviderSettings: otelcol.ConfigProviderSettings{
			ResolverSettings: confmap.ResolverSettings{
				URIs:              []string{"file:" + rpmConfig},
				ProviderFactories: []confmap.ProviderFactory{fileprovider.NewFactory(), envprovider.NewFactory()},
				DefaultScheme:     "env",
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, col.DryRun(context.Background()))
}
