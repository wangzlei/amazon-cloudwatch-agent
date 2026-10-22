// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || arm64)

package obireceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"

	"go.opentelemetry.io/obi/pkg/config"
)

func TestFactoryType(t *testing.T) {
	assert.Equal(t, TypeStr, NewFactory().Type().String())
}

func TestDefaults(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	assert.Equal(t, config.ContextPropagationTCPServiceName, cfg.OBI().EBPF.ContextPropagation)
	assert.False(t, cfg.OBI().NetworkFlows.Enable)
	assert.False(t, cfg.OBI().EnforceSysCaps)
	// Nothing is instrumented by default: the user must select targets
	// (discovery.instrument / open_port / ...), so the bare default is invalid.
	require.Error(t, cfg.Validate())
}

func TestUnmarshalOverridesDefaults(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	conf := confmap.NewFromStringMap(map[string]any{
		"ebpf": map[string]any{"context_propagation": "disabled"},
		"discovery": map[string]any{
			"instrument": []any{map[string]any{"open_ports": "8080"}},
		},
	})
	require.NoError(t, cfg.Unmarshal(conf))
	assert.Equal(t, config.ContextPropagationDisabled, cfg.OBI().EBPF.ContextPropagation)
	// untouched defaults survive
	assert.False(t, cfg.OBI().NetworkFlows.Enable)
	require.NoError(t, cfg.Validate())
}

func TestDefaultsAreIndependent(t *testing.T) {
	a := NewFactory().CreateDefaultConfig().(*Config)
	b := NewFactory().CreateDefaultConfig().(*Config)
	a.OBI().EBPF.ContextPropagation = config.ContextPropagationAll
	assert.Equal(t, config.ContextPropagationTCPServiceName, b.OBI().EBPF.ContextPropagation)
}
