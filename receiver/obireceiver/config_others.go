// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build !(linux && (amd64 || arm64))

package obireceiver // import "github.com/aws/amazon-cloudwatch-agent/receiver/obireceiver"

import (
	"go.opentelemetry.io/collector/component"

	"go.opentelemetry.io/obi/collector"
)

// Config is an empty placeholder on platforms OBI does not support.
type Config struct{}

func (*Config) Validate() error { return nil }

func createDefaultConfig() component.Config { return &Config{} }

var (
	createTraces  = collector.BuildTracesReceiver()
	createMetrics = collector.BuildMetricsReceiver()
)
