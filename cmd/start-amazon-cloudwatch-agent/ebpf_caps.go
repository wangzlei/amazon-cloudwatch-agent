// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aws/amazon-cloudwatch-agent/tool/paths"
)

// Capabilities of the eBPF (ADOT OBI) receiver, by feature. The systemd drop-in
// grants only the tiers the eBPF config uses ("-ebpf-caps", called by
// amazon-cloudwatch-agent-ctl when it installs the drop-in). A feature whose
// capabilities are missing degrades (logged) instead of failing the agent.
var (
	// RED metrics: load/attach BPF programs, read process memory and /proc.
	ebpfCapsRED = []string{"CAP_BPF", "CAP_PERFMON", "CAP_SYS_PTRACE", "CAP_DAC_READ_SEARCH", "CAP_CHECKPOINT_RESTORE", "CAP_NET_RAW"}
	// peer.service.name topology (ebpf.context_propagation, default
	// tcp_service_name): sockops/sk_msg programs (CAP_NET_ADMIN); CAP_SYS_ADMIN for
	// the FIONREAD compensation program on kernels with commit 929e30f93125.
	ebpfCapsTopology = []string{"CAP_NET_ADMIN", "CAP_SYS_ADMIN"}
	// Java agent injection (javaagent.enabled, default true): SIGQUIT to start
	// the JVM attach listener (CAP_KILL), setns into the JVM's net/ipc
	// (CAP_SYS_ADMIN) and mount (CAP_SYS_CHROOT) namespaces, and the attach
	// thread's switch to the JVM owner's uid/gid (CAP_SETUID, CAP_SETGID).
	ebpfCapsJava = []string{"CAP_SYS_ADMIN", "CAP_KILL", "CAP_SYS_CHROOT", "CAP_SETUID", "CAP_SETGID"}
	// Node.js injection (nodejs.enabled): SIGUSR1 to open the inspector.
	ebpfCapsNodeJS = []string{"CAP_KILL"}
)

// ebpfFeatures is the part of an OBI receiver config that decides the
// capability tiers. Absent keys keep the receiver's defaults (all on).
type ebpfFeatures struct {
	EBPF struct {
		ContextPropagation *string `yaml:"context_propagation"`
	} `yaml:"ebpf"`
	JavaAgent struct {
		Enabled *bool `yaml:"enabled"`
	} `yaml:"javaagent"`
	NodeJS struct {
		Enabled *bool `yaml:"enabled"`
	} `yaml:"nodejs"`
}

// ebpfCaps returns the capabilities for the OBI receivers ("obi" or "obi/<name>")
// in the given OTel config files, union over all of them, in a stable order.
// Any read or parse error returns an error: the caller then keeps the full set.
func ebpfCaps(files []string) ([]string, error) {
	var tiers [][]string
	tiers = append(tiers, ebpfCapsRED)
	found := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Receivers map[string]yaml.Node `yaml:"receivers"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for name, node := range doc.Receivers {
			if name != "obi" && !strings.HasPrefix(name, "obi/") {
				continue
			}
			found = true
			var fe ebpfFeatures
			if err := node.Decode(&fe); err != nil {
				return nil, fmt.Errorf("%s: receivers::%s: %w", f, name, err)
			}
			if fe.EBPF.ContextPropagation == nil || *fe.EBPF.ContextPropagation != "disabled" {
				tiers = append(tiers, ebpfCapsTopology)
			}
			if fe.JavaAgent.Enabled == nil || *fe.JavaAgent.Enabled {
				tiers = append(tiers, ebpfCapsJava)
			}
			if fe.NodeJS.Enabled == nil || *fe.NodeJS.Enabled {
				tiers = append(tiers, ebpfCapsNodeJS)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("no obi receiver in %v", files)
	}
	return unionCaps(tiers...), nil
}

// ebpfCapsAll is every tier: the grant when the config cannot be read.
func ebpfCapsAll() []string {
	return unionCaps(ebpfCapsRED, ebpfCapsTopology, ebpfCapsJava, ebpfCapsNodeJS)
}

func unionCaps(tiers ...[]string) []string {
	var caps []string
	seen := map[string]bool{}
	for _, t := range tiers {
		for _, c := range t {
			if !seen[c] {
				seen[c] = true
				caps = append(caps, c)
			}
		}
	}
	return caps
}

// ebpfConfigFiles are the OTel configs the agent loads when eBPF is on: the
// eBPF config and the YAMLs in etc/amazon-cloudwatch-agent.d (a customer YAML
// can add its own obi/<name> receiver).
func ebpfConfigFiles(agentDir, configDir string) []string {
	files := []string{filepath.Join(agentDir, "etc", "ebpf", "obi.yaml")}
	// same selection as config.GetOTELConfigArgs
	_ = filepath.WalkDir(configDir, func(path string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && filepath.Ext(path) == ".yaml" {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// printEBPFCaps implements "-ebpf-caps": the capability list for the systemd
// drop-in, space-separated. On any error it prints every tier (the previous
// fixed grant) and the error on stderr, so a broken config never leaves the
// drop-in without a capability line.
func printEBPFCaps() {
	caps, err := ebpfCaps(ebpfConfigFiles(paths.AgentDir, paths.ConfigDirPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "W! eBPF capabilities: %v; granting all tiers\n", err)
		caps = ebpfCapsAll()
	}
	fmt.Println(strings.Join(caps, " "))
}
