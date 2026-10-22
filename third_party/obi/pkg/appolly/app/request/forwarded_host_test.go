// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForwardedHostName(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"payment.ping.internal":      "payment.ping.internal",
		"payment.ping.internal:8000": "payment.ping.internal:8000",
		"a.example.com, proxy.local": "a.example.com",
		" spaced.example.com ":       "spaced.example.com",
		"[2001:db8::1]:443":          "[2001:db8::1]:443",
		"[2001:db8::1]":              "[2001:db8::1]",
		"10.0.10.246:8000":           "10.0.10.246:8000",
	} {
		assert.Equal(t, want, ForwardedHostName(in), in)
	}
}
