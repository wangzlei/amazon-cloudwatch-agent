// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || arm64)

package obireceiver // import "github.com/aws/amazon-cloudwatch-agent/receiver/obireceiver"

import (
	"go.opentelemetry.io/obi/pkg/config"
	"go.opentelemetry.io/obi/pkg/obi"
)

// DefaultOBIConfig returns OBI's defaults adjusted for the CloudWatch Agent:
//
//   - ebpf.context_propagation: "tcp_service_name". Servers return their service
//     name in a TCP option (-> peer.service.name on the client), only to peers
//     inside the VPC; no trace context is sent. "headers"/"all" are not the
//     default because they rewrite the application's HTTP payload, which is more
//     intrusive than a TCP option. Requires CAP_NET_ADMIN, and CAP_SYS_ADMIN on
//     kernels with commit 929e30f93125 (6.6.128+, 6.12.75+, 6.18.14+, 6.19+; the
//     FIONREAD compensation program); without them propagation is disabled and
//     RED still works.
//   - network flow metrics (network.enable) off: they are a separate,
//     high-cardinality feature that CloudWatch customers opt into explicitly.
//   - enforce_sys_caps false: missing capabilities are logged, and individual
//     features degrade, instead of failing agent startup.
//
// Everything else (RED metrics features, discovery defaults, buffer sizes) is
// OBI's upstream default.
func DefaultOBIConfig() *obi.Config {
	cfg := obi.DefaultConfig
	cfg.EBPF.ContextPropagation = config.ContextPropagationTCPServiceName
	cfg.NetworkFlows.Enable = false
	cfg.EnforceSysCaps = false
	return &cfg
}
