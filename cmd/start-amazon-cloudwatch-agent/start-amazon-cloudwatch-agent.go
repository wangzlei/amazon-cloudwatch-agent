// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"syscall"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/aws/amazon-cloudwatch-agent/cfg/envconfig"
	"github.com/aws/amazon-cloudwatch-agent/internal/constants"
	"github.com/aws/amazon-cloudwatch-agent/tool/paths"
)

func translateConfig() error {
	args := []string{"--output", paths.TomlConfigPath, "--mode", "auto"}
	if envconfig.IsRunningInContainer() {
		args = append(args, "--input-dir", paths.CONFIG_DIR_IN_CONTAINER)
	} else {
		args = append(args, "--input", paths.JsonConfigPath, "--input-dir", paths.ConfigDirPath, "--config", paths.CommonConfigPath)
	}
	cmd := exec.Command(paths.TranslatorBinaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			status := exitErr.Sys().(syscall.WaitStatus)
			switch {
			case status.Exited():
				log.Printf("I! Return exit error: exit code=%d\n", status.ExitStatus())

				if status.ExitStatus() == constants.ExitCodeNoJSONFile {
					log.Printf("I! No json config files found, please provide config, exit now\n")
					os.Exit(0)
				}
			}
		} else {
			log.Printf("Return other error: %s\n", err)
		}
	}

	return err
}

func main() {
	// "-ebpf-caps": print the capabilities the eBPF config needs (for the
	// systemd drop-in written by amazon-cloudwatch-agent-ctl) and exit.
	if len(os.Args) > 1 && os.Args[1] == "-ebpf-caps" {
		printEBPFCaps()
		return
	}

	var writer io.WriteCloser

	if !envconfig.IsRunningInContainer() {
		writer = &lumberjack.Logger{
			Filename:   paths.AgentLogFilePath,
			MaxSize:    100, //MB
			MaxBackups: 5,   //backup files
			MaxAge:     7,   //days
			Compress:   true,
		}

		log.SetOutput(writer)
	}

	// "-translate-only": the eBPF systemd drop-in runs this as root
	// (ExecStartPre=+) before the agent starts as the unprivileged cwagent user,
	// so that the translated config is written by root and etc/ stays root-owned
	// (cwagent can read it, not change it). The outputs of an earlier run are
	// removed first: when there is no JSON config any more, the agent must not
	// start with a stale TOML.
	translateOnly := len(os.Args) > 1 && os.Args[1] == "-translate-only"
	if translateOnly {
		for _, p := range []string{paths.TomlConfigPath, paths.YamlConfigPath} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				log.Fatalf("E! Cannot remove %s: %v \n", p, err)
			}
		}
	}

	if !translateOnly && !envconfig.IsRunningInContainer() && startedUnprivileged() {
		// Started unprivileged: only by the eBPF drop-in, which already ran
		// "-translate-only" as root. cwagent cannot write etc/.
		if _, err := os.Stat(paths.TomlConfigPath); errors.Is(err, fs.ErrNotExist) {
			// the root translation step already logged "No json config files found"
			log.Printf("I! No translated config %s, nothing to run, exit now\n", paths.TomlConfigPath)
			os.Exit(0)
		}
		log.Printf("I! Running as uid %d: using the config translated by root %s \n", os.Geteuid(), paths.TomlConfigPath)
	} else if err := translateConfig(); err != nil {
		log.Fatalf("E! Cannot translate JSON, ERROR is %v \n", err)
	}
	if translateOnly {
		log.Printf("I! Config has been translated into TOML %s (translate only) \n", paths.TomlConfigPath)
		return
	}
	log.Printf("I! Config has been translated into TOML %s \n", paths.TomlConfigPath)
	printFileContents(paths.TomlConfigPath)
	log.Printf("I! Config has been translated into YAML %s \n", paths.YamlConfigPath)
	printFileContents(paths.YamlConfigPath)

	if err := startAgent(writer); err != nil {
		log.Printf("E! Error when starting Agent, Error is %v \n", err)
		os.Exit(1)
	}
}

func printFileContents(path string) {
	file, err := os.Open(path)
	if err != nil {
		// YAML file may or may not exist and that is okay.
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("E! Error when printing file(%s) contents, Error is %v \n", path, err)
		}
		return
	}
	defer func() {
		if err = file.Close(); err != nil {
			log.Printf("E! Error when closing file, Error is %v \n", err)
		}
	}()

	b, err := io.ReadAll(file)
	if err != nil {
		log.Printf("E! Error when reading file(%s), Error is %v \n", path, err)
	}
	log.Printf("D! config %v", string(b))
}
