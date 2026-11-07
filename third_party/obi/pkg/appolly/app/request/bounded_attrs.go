// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request // import "go.opentelemetry.io/obi/pkg/appolly/app/request"

import (
	"sync"

	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
)

// Attributes whose value comes from request data (a client-sent Host /
// X-Forwarded-Host header, or the resource of an AWS SDK call) are validated and
// bounded before they become metric (and span) attributes: a remote client must
// not be able to create an unbounded number of metric series, or put arbitrary
// strings, into the customer's metrics.
//
//   - a value that is not a plausible host[:port] / AWS resource is dropped
//     (the attribute is omitted);
//   - per instrumented service, only the first maxDistinctPeerNames server-side
//     peer.service.name values (and maxDistinctAWSResources values of each AWS
//     resource attribute) are kept as they are; any further value is reported
//     as OverflowAttrValue.
const (
	maxDistinctPeerNames    = 50
	maxDistinctAWSResources = 100
	// OverflowAttrValue replaces a value beyond the per-service limit.
	OverflowAttrValue = "_other"
	// bound on the tracked (service, attribute) pairs; on overflow the tracking
	// restarts (a service may then admit up to the limit again).
	maxBoundedKeys = 10000

	maxHostPortLen = 253 + 1 + 5 // DNS name, ':', port
	maxAWSValueLen = 256
)

type boundedKey struct {
	svc  svc.UID
	attr string
}

type boundedValues struct {
	mu   sync.Mutex
	seen map[boundedKey]map[string]struct{}
}

var bounded = boundedValues{seen: map[boundedKey]map[string]struct{}{}}

// admit returns v if it is one of the first limit distinct values of attr for the
// service, else OverflowAttrValue.
func (b *boundedValues) admit(service svc.UID, attr, v string, limit int) string {
	k := boundedKey{svc: service, attr: attr}
	b.mu.Lock()
	defer b.mu.Unlock()
	vals, ok := b.seen[k]
	if !ok {
		if len(b.seen) >= maxBoundedKeys {
			b.seen = map[boundedKey]map[string]struct{}{}
		}
		vals = map[string]struct{}{}
		b.seen[k] = vals
	}
	if _, ok := vals[v]; ok {
		return v
	}
	if len(vals) >= limit {
		return OverflowAttrValue
	}
	vals[v] = struct{}{}
	return v
}

func (b *boundedValues) reset() {
	b.mu.Lock()
	b.seen = map[boundedKey]map[string]struct{}{}
	b.mu.Unlock()
}

// validHostPort accepts a DNS name or IP literal, optionally with ":port", as
// sent in Host / X-Forwarded-Host: letters, digits, '.', '-', '_', ':' and the
// IPv6 brackets, at most 259 bytes.
func validHostPort(v string) bool {
	if v == "" || len(v) > maxHostPortLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	return true
}

// validAWSValue accepts the characters of S3 bucket names, SQS queue URLs and
// Lambda function names / ARNs / regions, at most 256 bytes.
func validAWSValue(v string) bool {
	if v == "" || len(v) > maxAWSValueLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == ':', c == '/', c == '$':
		default:
			return false
		}
	}
	return true
}

// serverPeerServiceName is the bounded server-side peer.service.name: the
// validated front-door name from X-Forwarded-Host / Host, "" if invalid.
func serverPeerServiceName(s *Span) string {
	name := ForwardedHostName(s.ForwardedHost)
	if name == "" {
		return ""
	}
	return bounded.admit(s.Service.UID, "peer.service.name", name, maxDistinctPeerNames)
}

// boundedAWSValue validates and bounds an AWS resource attribute value of the
// span's service; "" when invalid.
func boundedAWSValue(s *Span, attr, v string) string {
	if !validAWSValue(v) {
		return ""
	}
	return bounded.admit(s.Service.UID, attr, v, maxDistinctAWSResources)
}
