// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package runtimecontrol

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	defaultPollInterval = 300 * time.Second
	minPollInterval     = 10 * time.Second
)

// SSM parameter names: letters, digits, _ . - and / (hierarchies), at most 2048 characters.
var parameterName = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// Config of the runtimecontrol extension.
type Config struct {
	// Parameter is the name (or ARN) of the SSM parameter that holds the runtime switches.
	// Empty disables the extension: no AWS calls, every flag is unknown (gates use their default).
	Parameter string `mapstructure:"parameter"`
	// PollInterval between two GetParameter calls (each delay is jittered by +-20%).
	PollInterval time.Duration `mapstructure:"poll_interval"`
	// Region of the parameter; empty = the SDK's region resolution (AWS_REGION, profile, ...).
	Region string `mapstructure:"region"`
	// Profile and SharedCredentialsFile: optional, as in the agent's common-config.toml.
	// Empty = the SDK default credential chain (environment, shared config, EC2/ECS/EKS role).
	Profile               string `mapstructure:"profile"`
	SharedCredentialsFile string `mapstructure:"shared_credentials_file"`
}

var _ component.Config = (*Config)(nil)

// Validate accepts every value: a bad parameter name or poll interval must not stop the agent
// (and its RED metrics) from starting. Start disables the extension for an invalid name (W!)
// and raises a too short interval to the minimum (W!); see problems.
func (c *Config) Validate() error { return nil }

// problems returns the effective poll interval and why the config cannot be used as is.
func (c *Config) problems() (interval time.Duration, invalidName error, intervalRaised bool) {
	interval = c.PollInterval
	if len(c.Parameter) > 2048 || (!parameterName.MatchString(c.Parameter) && !isARN(c.Parameter)) {
		invalidName = fmt.Errorf("parameter %q is not a valid SSM parameter name or ARN", c.Parameter)
	}
	if interval < minPollInterval {
		interval, intervalRaised = minPollInterval, true
	}
	return interval, invalidName, intervalRaised
}

func isARN(s string) bool {
	return strings.HasPrefix(s, "arn:") && !strings.ContainsAny(s, " \t\n")
}
