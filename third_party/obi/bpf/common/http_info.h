// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

#pragma once

#include <bpfcore/vmlinux.h>

#include <common/connection_info.h>
#include <common/event_source.h>
#include <common/tp_info.h>

#define FULL_BUF_SIZE 256

// EXPERIMENTAL — TCP service-name propagation. Must match K_SVC_NAME_MAX_LEN in
// bpf/maps/svc_peer_name_map.h. Defined here (the widely-included event header)
// so both http_info_t and http_request_trace_t can size their peer-name field.
#define HTTP_PEER_SVC_NAME_LEN 25

// Here we keep the information that is sent on the ring buffer
typedef struct http_info {
    u8 flags; // Must be first, we use it to tell what kind of packet we have on the ring buffer
    u8 type;
    u8 ssl;
    u8 delayed;
    connection_info_t conn_info;
    u64 start_monotime_ns;
    u64 end_monotime_ns;
    u64 req_monotime_ns;
    u64 extra_id;
    tp_info_t tp;
    pid_info pid;
    u32 len;
    u32 resp_len;
    u32 task_tid;
    u32 lb_req_bytes;
    u32 lb_res_bytes;
    u16 status;
    unsigned char buf[FULL_BUF_SIZE];
    // EXPERIMENTAL — TCP service-name propagation: downstream service name for a
    // CLIENT http_info, learned from a kind-26 TCP option on the response and
    // looked up by connection (see bpf/maps/svc_peer_name_map.h). 32-byte block
    // (25 + 1 + 6 pad) is a multiple of 8, so it preserves the alignment of every
    // field below it and the struct size (BPF is built -Werror -Wpadded).
    unsigned char peer_service_name[HTTP_PEER_SVC_NAME_LEN];
    u8 peer_service_name_len;
    u8 _pad_peer[6];
    u8 has_large_buffers;
    u8 direction;
    u8 submitted;
    enum parent_status parent_status;
    enum event_source_type event_source;
    u8 _pad[1];
} http_info_t;
