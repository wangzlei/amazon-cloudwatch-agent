// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/aws/amazon-cloudwatch-agent/internal/constants"
	"github.com/aws/amazon-cloudwatch-agent/tool/cmdwrapper"
	"github.com/aws/amazon-cloudwatch-agent/translator/flags"
)

func main() {
	fmt.Println("Starting config-translator, this will map back to a call to amazon-cloudwatch-agent")

	fs, translatorFlags := cmdwrapper.CreateFlagSet(flags.TranslatorCommand, flags.TranslatorFlags)
	fs.Parse(os.Args[1:]) // Skip program name only

	err := cmdwrapper.ExecuteSubcommand(flags.TranslatorCommand, translatorFlags)
	if err != nil {
		fmt.Println(err.Error())
		// Pass "no JSON config" through: start-amazon-cloudwatch-agent then
		// exits 0 instead of failing (and systemd restarting it in a loop).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == constants.ExitCodeNoJSONFile {
			os.Exit(constants.ExitCodeNoJSONFile)
		}
		os.Exit(1)
	}
}
