// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

#pragma once

#include <bpfcore/vmlinux.h>
#include <bpfcore/bpf_helpers.h>

#include <maps/svc_peer_name_map.h> // svc_name_value_t

// EXPERIMENTAL — TCP service-name propagation, write side.
//
// Per-socket state for emitting the local service name in a TCP option. An entry
// exists only on SERVER (passive) sockets: it is created at passive-established
// (marking the socket as a responder) and filled with the resolved local name
// the first time the server process writes response data through sk_msg.
//
// Presence of an entry is also how the sk_msg entry point distinguishes a server
// socket from a client one, so the client request path is left completely
// unchanged (it never gets an entry here).
enum {
    k_svc_state_server_pending = 1, // passive socket, local name not resolved yet
    k_svc_state_ready = 2,          // name resolved; opt_len/write_hdr may emit it
};

typedef struct sk_svc_name_state {
    svc_name_value_t name; // resolved local service name (valid when state == ready)
    u8 state;
} sk_svc_name_state_t;

struct {
    __uint(type, BPF_MAP_TYPE_SK_STORAGE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __type(key, u32); // matches sk_tp_info_pid_map style (sk storage, key size 4)
    __type(value, sk_svc_name_state_t);
} sk_svc_name_map SEC(".maps");
