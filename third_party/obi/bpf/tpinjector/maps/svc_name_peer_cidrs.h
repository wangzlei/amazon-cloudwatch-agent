// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

#pragma once

#include <bpfcore/vmlinux.h>
#include <bpfcore/bpf_helpers.h>

#include <common/pin_internal.h>

enum { k_svc_name_peer_cidrs_max = 256 };

// IPv4 peers are matched as IPv4-mapped IPv6 (::ffff:a.b.c.d), the same layout as
// connection_info_t addresses, so one trie holds both families.
typedef struct svc_name_peer_cidr_key {
    u32 prefixlen;
    u8 addr[16];
} svc_name_peer_cidr_key_t;

// EXPERIMENTAL — TCP service-name propagation. The networks a server may send its
// service name to (the host's VPC CIDRs, or the private ranges off AWS). Filled
// from user space at startup; unused when svc_name_any_peer is set.
struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __type(key, svc_name_peer_cidr_key_t);
    __type(value, u8);
    __uint(max_entries, k_svc_name_peer_cidrs_max);
    __uint(pinning, OBI_PIN_INTERNAL);
} svc_name_peer_cidrs SEC(".maps");
