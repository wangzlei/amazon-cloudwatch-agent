// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package gate

import (
	"errors"

	"go.opentelemetry.io/collector/component"
)

// Config of the gate processor.
type Config struct {
	// Flag is the name of the runtime switch (a key of the runtimecontrol extension's
	// SSM parameter), e.g. traces_export.
	Flag string `mapstructure:"flag"`
	// Default is used while the switch is unknown: no runtimecontrol extension, no
	// parameter configured, not read yet, or the key absent from the parameter.
	Default bool `mapstructure:"default"`
	// Extension selects the runtimecontrol extension (e.g. runtimecontrol/cwagent_ebpf).
	// Empty: the only one in service::extensions.
	Extension *component.ID `mapstructure:"extension"`
}

var _ component.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Flag == "" {
		return errors.New("flag must be set")
	}
	return nil
}
