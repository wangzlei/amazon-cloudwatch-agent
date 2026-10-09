// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

//go:build linux || darwin
// +build linux darwin

package main

import (
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

	if os.Geteuid() != 0 {
		// Started unprivileged (e.g. systemd User=cwagent with ambient capabilities
		// for the eBPF receiver): there is no user to switch to, and the chown and
		// setgroups that ChangeUser does would fail without root.
		log.Printf("I! Running as uid %d, not root: skipping run_as_user %q", os.Geteuid(), runAsUser)
	} else if _, err = user.ChangeUser(runAsUser); err != nil {
		log.Printf("E! Failed to ChangeUser: %v ", err)
		return err
	}

	name, err := exec.LookPath(paths.AgentBinaryPath)
	if err != nil {
		log.Printf("E! Failed to lookpath: %v ", err)
		return err
	}

	// Before the log file is closed: it logs whether the eBPF config is used.
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

// ebpfConfigArgs returns the -otelconfig pair for the default eBPF (ADOT OBI)
// config shipped by the RPM in etc/ebpf/, unless etc/ebpf/disabled exists. It
// is kept outside amazon-cloudwatch-agent.d so that fetch-config, which empties
// that directory, does not delete it. Note that a YAML in
// amazon-cloudwatch-agent.d cannot redefine the components it declares: the
// merge rejects duplicate keys in receivers, processors, exporters, extensions
// and service::pipelines. Edit etc/ebpf/obi.yaml (%config(noreplace)) to change
// it, or create etc/ebpf/disabled to turn it off.
func ebpfConfigArgs() []string {
	cfg := filepath.Join(paths.AgentDir, "etc", "ebpf", "obi.yaml")
	if _, err := os.Stat(cfg); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(paths.AgentDir, "etc", "ebpf", "disabled")); err == nil {
		log.Printf("I! eBPF default config %s is disabled (etc/ebpf/disabled exists)", cfg)
		return nil
	}
	log.Printf("I! Loading eBPF default config %s", cfg)
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
