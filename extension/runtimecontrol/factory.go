// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

// Package runtimecontrol is an extension that polls one SSM parameter for runtime switches
// (JSON object of booleans, e.g. {"version":1,"traces_export":true,"emf_export":false}) and
// exposes them in-process to processors such as gate, so that exports can be turned on and
// off without restarting the agent. It is the only component that calls SSM.
package runtimecontrol

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

var TypeStr = component.MustNewType("runtimecontrol")

func NewFactory() extension.Factory {
	return extension.NewFactory(
		TypeStr,
		createDefaultConfig,
		createExtension,
		component.StabilityLevelAlpha,
	)
}

func createDefaultConfig() component.Config {
	return &Config{PollInterval: defaultPollInterval}
}

func createExtension(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	return newExtension(cfg.(*Config), set.Logger, set.ID.String(), nil), nil
}
