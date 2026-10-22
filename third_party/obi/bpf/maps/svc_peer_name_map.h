// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

#pragma once

#include <bpfcore/vmlinux.h>
#include <bpfcore/bpf_helpers.h>

#include <common/connection_info.h>
#include <common/map_sizing.h>
#include <common/pin_internal.h>

// EXPERIMENTAL — TCP service-name propagation (see docs/TCP_SERVICE_NAME_PROPAGATION.md).
//
// The peer's OpenTelemetry service.name, learned from a TCP option that the peer
// wrote on a segment it sent. It carries a hop-by-hop identity: "the agent
// managing the endpoint that sent this segment calls it <name>". It is NOT the
// logical HTTP target, the LB-selected backend, or a per-stream identity.
//
// Wire limit is 25 bytes (the TCP option is 28 bytes total: kind+len+version+25;
// established connections leave ~28 bytes of option space with timestamps on).
// Names longer than 25 bytes are rejected, never truncated, because truncation
// could merge unrelated services.
#define K_SVC_NAME_MAX_LEN 25

// All-u8, so alignment is 1 and there is no padding (BPF is built -Werror -Wpadded).
typedef struct svc_name_value {
    unsigned char name[K_SVC_NAME_MAX_LEN];
    u8 len; // valid bytes in name; 0 means "unset"
} svc_name_value_t;

// Peer service name keyed by the sorted connection tuple. Written by the sockops
// parse callback on the side that RECEIVES a peer's name (a client reading it off
// a response segment); read by the client-span producer in the per-language
// tracers, exactly like incoming_trace_map. Shared+pinned so the tpinjector
// object and the tracer objects see the same map.
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __type(key, connection_info_t);
    __type(value, svc_name_value_t);
    __uint(max_entries, MAX_CONCURRENT_REQUESTS);
    __uint(pinning, OBI_PIN_INTERNAL);
} svc_peer_name_map SEC(".maps");
