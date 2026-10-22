// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package obireceiver is the CloudWatch Agent entry point for the ADOT-patched
// OpenTelemetry eBPF Instrumentation (OBI).
//
// It is a thin wrapper over OBI's own collector receiver
// (go.opentelemetry.io/obi/collector): the create functions, the shared
// traces/metrics controller and the eBPF pipeline are OBI's. What this package
// adds are CloudWatch-Agent-friendly defaults (see defaults_linux.go) and
// receiver-mode validation. On platforms OBI does not support the factory
// still registers (so cwagent's component list is identical on every OS) but
// creating the receiver fails with OBI's "unsupported platform" error.
package obireceiver // import "github.com/aws/amazon-cloudwatch-agent/receiver/obireceiver"

import (
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

// TypeStr is the component type used in the agent's OTel YAML (receivers: obi:).
const TypeStr = "obi"

var componentType = component.MustNewType(TypeStr)

// NewFactory returns the OBI receiver factory with CloudWatch Agent defaults.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		componentType,
		createDefaultConfig,
		receiver.WithTraces(createTraces, component.StabilityLevelDevelopment),
		receiver.WithMetrics(createMetrics, component.StabilityLevelDevelopment),
	)
}
