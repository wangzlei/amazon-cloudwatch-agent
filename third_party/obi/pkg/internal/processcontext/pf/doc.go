// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package pf holds the few go.opentelemetry.io/ebpf-profiler pieces the
// process-context decorator needs, copied verbatim from ebpf-profiler
// v0.0.202633: libpf.Address/libpf.PID, libpf/pfunsafe and remotememory.
//
// ADOT-OBI (patch 99-pin-to-cwagent): every ebpf-profiler release that fits the
// CloudWatch Agent's collector line (<= v1.56.0) still requires golang.org/x/arch
// >= v0.23.0 and mdlayher/socket v0.5.1, and OBI used nothing else from it, so
// the dependency is dropped instead of pinned.
package pf // import "go.opentelemetry.io/obi/pkg/internal/processcontext/pf"

// Address represents an address, or offset within a process (libpf.Address).
type Address uintptr

// PID represent Unix Process ID (libpf.PID).
type PID uint32
