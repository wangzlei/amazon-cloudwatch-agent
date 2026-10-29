// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

//go:build linux || darwin
// +build linux darwin

package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/aws/amazon-cloudwatch-agent/cfg/envconfig"
	"github.com/aws/amazon-cloudwatch-agent/internal/util/config"
	"github.com/aws/amazon-cloudwatch-agent/internal/util/user"
	"github.com/aws/amazon-cloudwatch-agent/tool/paths"
)

func startAgent(writer io.WriteCloser) error {
	if envconfig.IsRunningInContainer() {
		// Use exec so PID 1 changes to agent from start-agent.
		execArgs := []string{
			paths.AgentBinaryPath, // when using syscall.Exec, must pass binary name as args[0]
			"-config", paths.TomlConfigPath,
			"-envconfig", paths.EnvConfigPath,
		}
		// eBPF receiving pipeline (etc/ebpf/receiver.yaml) when opted in, as on the host below.
		execArgs = append(execArgs, ebpfConfigArgs()...)
		execArgs = append(execArgs, config.GetOTELConfigArgs(paths.CONFIG_DIR_IN_CONTAINER)...)
		execArgs = append(execArgs, "-pidfile", paths.AgentDir+"/var/amazon-cloudwatch-agent.pid")
		if err := syscall.Exec(paths.AgentBinaryPath, execArgs, os.Environ()); err != nil {
			return fmt.Errorf("error exec as agent binary: %w", err)
		}
		// We should never reach this line but the compiler doesn't know...
		return nil
	}

	configMap, err := getTOMLConfigMap()
	if err != nil {
		log.Printf("E! Failed to read TOML config: %v ", err)
		return err
	}

	runAsUser, _ := user.DetectRunAsUser(configMap)
	log.Printf("I! Detected runAsUser: %v", runAsUser)

	_, err = user.ChangeUser(runAsUser)
	if err != nil {
		log.Printf("E! Failed to ChangeUser: %v ", err)
		return err
	}

	name, err := exec.LookPath(paths.AgentBinaryPath)
	if err != nil {
		log.Printf("E! Failed to lookpath: %v ", err)
		return err
	}

	// Before the log file is closed: it logs whether the eBPF pipeline is used.
	ebpfArgs := ebpfConfigArgs()

	if err := writer.Close(); err != nil {
		log.Printf("E! Cannot close the log file, ERROR is %v ", err)
		return err
	}

	// linux command has pid passed while windows does not
	agentCmd := []string{
		paths.AgentBinaryPath,
		"-config", paths.TomlConfigPath,
		"-envconfig", paths.EnvConfigPath,
	}
	agentCmd = append(agentCmd, ebpfArgs...)
	agentCmd = append(agentCmd, config.GetOTELConfigArgs(paths.ConfigDirPath)...)
	agentCmd = append(agentCmd, "-pidfile", paths.AgentDir+"/var/amazon-cloudwatch-agent.pid")
	if err = syscall.Exec(name, agentCmd, os.Environ()); err != nil {
		// log file is closed, so use fmt here
		fmt.Printf("E! Exec failed: %v \n", err)
		return err
	}

	return nil
}

// ebpfConfigArgs returns the -otelconfig pair for the agent's eBPF receiving pipeline
// (etc/ebpf/receiver.yaml), but only when eBPF is opted in: etc/ebpf/enabled exists
// (created by "amazon-cloudwatch-agent-ctl -a enable-ebpf", which also enables the eBPF
// unit) or, in a container, CWAGENT_EBPF_ENABLED=true. Without it the agent behaves like
// the official one. The pipeline receives OTLP from the eBPF process
// (amazon-cloudwatch-agent-ebpf, OBI) on the Unix socket var/ebpf/otlp.sock. The config
// is kept outside amazon-cloudwatch-agent.d so that fetch-config, which empties that
// directory, does not delete it; a YAML in amazon-cloudwatch-agent.d cannot redefine the
// components it declares (the merge rejects duplicate keys).
//
// When enabled it also prepares the socket: a socket left by a killed agent makes the
// receiver fail with "address already in use", so it is removed, and the agent runs with
// umask 007 so the socket it creates in the setgid var/ebpf directory (group cwagent) is
// connectable by its owner and the cwagent group (the eBPF unit's user) only.
func ebpfConfigArgs() []string {
	cfg := filepath.Join(paths.AgentDir, "etc", "ebpf", "receiver.yaml")
	marker := filepath.Join(paths.AgentDir, "etc", "ebpf", "enabled")
	socket := filepath.Join(paths.AgentDir, "var", "ebpf", "otlp.sock")
	if _, err := os.Stat(marker); err != nil && os.Getenv(envconfig.CWAgentEBPFEnabled) != "true" {
		return nil
	}
	if _, err := os.Stat(cfg); err != nil {
		log.Printf("W! eBPF is enabled but %s is missing: %v", cfg, err)
		return nil
	}
	if err := os.Remove(socket); err == nil {
		log.Printf("I! Removed the eBPF socket %s left by a previous run", socket)
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("W! Cannot remove the eBPF socket %s: %v", socket, err)
	}
	syscall.Umask(0o007)
	log.Printf("I! eBPF is enabled: loading the eBPF receiving pipeline %s", cfg)
	return []string{"-otelconfig", cfg}
}

func getTOMLConfigMap() (map[string]any, error) {
	f, err := os.Open(paths.TomlConfigPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var m map[string]any
	_, err = toml.NewDecoder(f).Decode(&m)
	if err != nil {
		return nil, err
	}
	return m, nil
}
