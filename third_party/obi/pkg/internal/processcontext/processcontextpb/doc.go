// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package v1development is a verbatim copy of the generated
// go.opentelemetry.io/proto/otlp/processcontext/v1development v0.4.0
// (process_context.pb.go).
//
// ADOT-OBI (patch 99-pin-to-cwagent): every release of that module requires
// go.opentelemetry.io/proto/otlp >= v1.11.0, which would bump the CloudWatch
// Agent's proto/otlp (v1.10.0), grpc-gateway and genproto. The generated code
// only needs otlp common/v1 and resource/v1, which v1.10.0 provides.
package v1development // import "go.opentelemetry.io/obi/pkg/internal/processcontext/processcontextpb"
