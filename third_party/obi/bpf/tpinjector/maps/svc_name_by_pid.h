// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

#pragma once

#include <bpfcore/vmlinux.h>
#include <bpfcore/bpf_helpers.h>

#include <common/map_sizing.h>
#include <common/pin_internal.h>
#include <maps/svc_peer_name_map.h> // svc_name_value_t

// EXPERIMENTAL — TCP service-name propagation.
//
// The local service.name of each instrumented process, keyed by host PID (the
// tgid returned by pid_from_pid_tgid). Populated from user space in
// tpinjector.AllowPID at process discovery (where svc.Attrs is resolved) and
// removed in BlockPID. The sk_msg send path looks it up with the writing
// process's PID so the option carries a real service name, not a node-global
// one. This is the "identity availability" piece the design calls out.
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, u32); // host PID (tgid)
    __type(value, svc_name_value_t);
    __uint(max_entries, MAX_CONCURRENT_REQUESTS);
    __uint(pinning, OBI_PIN_INTERNAL);
} svc_name_by_pid SEC(".maps");
