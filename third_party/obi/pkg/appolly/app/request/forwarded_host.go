// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request // import "go.opentelemetry.io/obi/pkg/appolly/app/request"

import "strings"

// ForwardedHostName turns a raw X-Forwarded-Host / Host header value into the
// name the client used for this server: the first entry of a comma-separated proxy
// chain, kept as sent (port included), so it equals the client's server.address.
// Returns "" for an empty value.
func ForwardedHostName(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
