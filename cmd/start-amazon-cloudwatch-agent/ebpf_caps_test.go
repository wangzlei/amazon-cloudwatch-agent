// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestEBPFCaps(t *testing.T) {
	red := ebpfCapsRED
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"defaults: every tier", "receivers:\n  obi:\n    ebpf: {}\n",
			unionCaps(red, ebpfCapsTopology, ebpfCapsJava, ebpfCapsNodeJS)},
		{"java on, nodejs off (shipped default)", "receivers:\n  obi/x:\n    nodejs:\n      enabled: false\n",
			unionCaps(red, ebpfCapsTopology, ebpfCapsJava)},
		{"java and nodejs off", "receivers:\n  obi/x:\n    javaagent: {enabled: false}\n    nodejs: {enabled: false}\n",
			unionCaps(red, ebpfCapsTopology)},
		{"nothing but RED", "receivers:\n  obi/x:\n    ebpf: {context_propagation: disabled}\n    javaagent: {enabled: false}\n    nodejs: {enabled: false}\n",
			red},
		{"nodejs only adds CAP_KILL", "receivers:\n  obi/x:\n    ebpf: {context_propagation: disabled}\n    javaagent: {enabled: false}\n",
			unionCaps(red, []string{"CAP_KILL"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := writeYAML(t, t.TempDir(), "obi.yaml", tt.yaml)
			got, err := ebpfCaps([]string{f})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "")
		})
	}
}

func TestEBPFCapsUnionAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeYAML(t, dir, "a.yaml", "receivers:\n  obi/a:\n    ebpf: {context_propagation: disabled}\n    javaagent: {enabled: false}\n    nodejs: {enabled: false}\n")
	b := writeYAML(t, dir, "b.yaml", "receivers:\n  otlp: {}\n  obi/b:\n    ebpf: {context_propagation: disabled}\n    nodejs: {enabled: false}\n")
	got, err := ebpfCaps([]string{a, b})
	require.NoError(t, err)
	assert.Equal(t, unionCaps(ebpfCapsRED, ebpfCapsJava), got)
}

func TestEBPFCapsErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ebpfCaps([]string{filepath.Join(dir, "missing.yaml")})
	assert.Error(t, err)
	_, err = ebpfCaps([]string{writeYAML(t, dir, "bad.yaml", "receivers: [")})
	assert.Error(t, err)
	_, err = ebpfCaps([]string{writeYAML(t, dir, "none.yaml", "receivers:\n  otlp: {}\n")})
	assert.Error(t, err)
	assert.Len(t, ebpfCapsAll(), 12)
}

// The shipped default config: Java injection on, Node.js off -> every tier but
// Node.js, which adds nothing beyond Java (CAP_KILL): all 12 capabilities.
func TestEBPFCapsShippedConfig(t *testing.T) {
	got, err := ebpfCaps([]string{"../../packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml"})
	require.NoError(t, err)
	assert.Equal(t, unionCaps(ebpfCapsRED, ebpfCapsTopology, ebpfCapsJava), got)
}

func TestEBPFConfigFiles(t *testing.T) {
	agent := t.TempDir()
	conf := filepath.Join(agent, "etc", "amazon-cloudwatch-agent.d")
	require.NoError(t, os.MkdirAll(conf, 0o755))
	writeYAML(t, conf, "x.yaml", "")
	writeYAML(t, conf, "y.json", "")
	got := ebpfConfigFiles(agent, conf)
	assert.Equal(t, []string{filepath.Join(agent, "etc", "ebpf", "obi.yaml"), filepath.Join(conf, "x.yaml")}, got)
}
