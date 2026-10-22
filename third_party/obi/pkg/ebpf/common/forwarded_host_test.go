// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ebpfcommon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForwardedHostFromBuf(t *testing.T) {
	for _, tc := range []struct {
		name, req, want string
	}{
		{"host only", "POST /charges HTTP/1.1\r\nHost: payment.ping.internal:8000\r\nAccept: */*\r\n\r\n", "payment.ping.internal:8000"},
		{"x-forwarded-host wins", "GET / HTTP/1.1\r\nHost: 10.0.10.246:8000\r\nX-Forwarded-Host: shop.example.com\r\n\r\n", "shop.example.com"},
		{"case-insensitive", "GET / HTTP/1.1\r\nx-forwarded-host: a.b\r\nhost: c.d\r\n\r\n", "a.b"},
		{"alb header order", "POST /charges HTTP/1.1\r\nX-Forwarded-For: 10.0.6.247\r\nX-Forwarded-Proto: http\r\nX-Forwarded-Port: 80\r\nHost: payment.ping.internal\r\nX-Amzn-Trace-Id: Root=1-abc\r\n\r\n", "payment.ping.internal"},
		{"truncated value ignored", "GET / HTTP/1.1\r\nHost: payment.ping.inter", ""},
		{"truncated x-forwarded-host falls back to host", "GET / HTTP/1.1\r\nHost: a.b\r\nX-Forwarded-Host: c.", "a.b"},
		{"header in body ignored", "POST / HTTP/1.1\r\nHost: a.b\r\n\r\nX-Forwarded-Host: evil\r\n", "a.b"},
		{"similar header name", "GET / HTTP/1.1\r\nHostname: x\r\nHost: a.b\r\n\r\n", "a.b"},
		{"nul padded buffer", "GET / HTTP/1.1\r\nHost: a.b\r\n\x00\x00\x00", "a.b"},
		{"no host", "GET / HTTP/1.1\r\nAccept: */*\r\n\r\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, forwardedHostFromBuf([]byte(tc.req)))
		})
	}
}
