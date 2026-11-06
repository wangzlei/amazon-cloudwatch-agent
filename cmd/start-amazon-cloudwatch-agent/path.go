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
		// eBPF default config (etc/ebpf/obi.yaml) when opted in, as on the host below.
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

	if os.Geteuid() != 0 {
		// Started unprivileged: only when eBPF is enabled, whose systemd drop-in
		// runs the agent as User=cwagent with ambient capabilities for the eBPF
		// receiver. There is no user to switch to, and the chown and setgroups
		// that ChangeUser does would fail without root.
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

// startedUnprivileged reports whether the launcher was started as a non-root
// user: only by the eBPF systemd drop-in (User=cwagent).
func startedUnprivileged() bool {
	return os.Geteuid() != 0
}

// ebpfConfigArgs returns the -otelconfig pair for the default eBPF (ADOT OBI)
// config shipped by the RPM in etc/ebpf/, but only when eBPF is opted in, i.e.
// etc/ebpf/enabled exists. The marker is created by
// "amazon-cloudwatch-agent-ctl -a enable-ebpf" (which also installs the systemd
// drop-in that grants the capabilities) and removed by "-a disable-ebpf". Without
// it the agent behaves like the official one. The config is kept outside
// amazon-cloudwatch-agent.d so that fetch-config, which empties that directory,
// does not delete it. Note that a YAML in amazon-cloudwatch-agent.d cannot
// redefine the components it declares: the merge rejects duplicate keys in
// receivers, processors, exporters, extensions and service::pipelines. Edit
// etc/ebpf/obi.yaml (%config(noreplace)) to change it.
func ebpfConfigArgs() []string {
	cfg := filepath.Join(paths.AgentDir, "etc", "ebpf", "obi.yaml")
	marker := filepath.Join(paths.AgentDir, "etc", "ebpf", "enabled")
	if _, err := os.Stat(marker); err != nil {
		return nil
	}
	if _, err := os.Stat(cfg); err != nil {
		log.Printf("W! eBPF is enabled (%s exists) but %s is missing: %v", marker, cfg, err)
		return nil
	}
	if !envconfig.IsRunningInContainer() {
		// The eBPF config decides what the privileged agent captures and where it
		// sends it: only root may change it. The drop-in keeps etc/ root-owned;
		// refuse a config the agent user (or anyone but root) could have written.
		for _, p := range []string{filepath.Dir(filepath.Dir(cfg)), filepath.Dir(cfg), cfg, marker} {
			if err := rootOwnedNotWritable(p); err != nil {
				log.Printf("E! Not loading the eBPF config %s: %v", cfg, err)
				return nil
			}
		}
	}
	log.Printf("I! eBPF is enabled (%s exists): loading eBPF default config %s", marker, cfg)
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

// rootOwnedNotWritable returns an error unless p is owned by root and not
// writable by its group or by others.
func rootOwnedNotWritable(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot read the owner", p)
	}
	if st.Uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root", p, st.Uid)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", p)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others (mode %v)", p, fi.Mode().Perm())
	}
	return nil
}
