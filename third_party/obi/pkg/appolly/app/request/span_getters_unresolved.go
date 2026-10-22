// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request // import "go.opentelemetry.io/obi/pkg/appolly/app/request"

import (
	"net"

	"go.opentelemetry.io/otel/attribute"

	"go.opentelemetry.io/obi/pkg/export/attributes"
	attr "go.opentelemetry.io/obi/pkg/export/attributes/names"
)

// awsMetadataIPs are the fixed link-local endpoints AWS SDKs and agents call for
// instance metadata and credentials: EC2 IMDS (IPv4/IPv6), the ECS task metadata and
// credentials endpoint, and the EKS Pod Identity agent (IPv4/IPv6). They never
// resolve to a name, but they are a handful of well-known constants, so keeping the
// IP costs no cardinality and is far more useful than "outgoing"/"unresolved".
var awsMetadataIPs = map[string]struct{}{
	"169.254.169.254": {},
	"fd00:ec2::254":   {},
	"169.254.170.2":   {},
	"169.254.170.23":  {},
	"fd00:ec2::23":    {},
}

func isAWSMetadataIP(ip net.IP) bool {
	_, ok := awsMetadataIPs[ip.String()]
	return ok
}

func willReplaceIP(value, replacement string) bool {
	if replacement == "" {
		return false
	}
	ip := net.ParseIP(value)
	return ip != nil && !isAWSMetadataIP(ip)
}

func unresolvedValue(value, replacement string) string {
	if willReplaceIP(value, replacement) {
		return replacement
	}

	return value
}

// otelUnresolvedHostGetters wraps spanOTELGetters but replacing client and server address
// unresolved metrics by a user-provided tag (usually "unresolved")
func otelUnresolvedHostGetters(unresolved UnresolvedNames) func(name attr.Name) (attributes.Getter[*Span, attribute.KeyValue], bool) {
	return func(name attr.Name) (attributes.Getter[*Span, attribute.KeyValue], bool) {
		getter, ok := spanOTELGetters(name)
		switch name {
		case attr.Client, attr.ClientAddr:
			return func(s *Span) attribute.KeyValue {
				kv := getter(s)
				if s.IsClientSpan() {
					kv.Value = attribute.StringValue(unresolvedValue(kv.Value.AsString(), unresolved.Generic))
				} else {
					kv.Value = attribute.StringValue(unresolvedValue(kv.Value.AsString(), unresolved.Incoming))
				}
				return kv
			}, true
		case attr.Server, attr.ServerAddr:
			return func(s *Span) attribute.KeyValue {
				kv := getter(s)
				if s.IsClientSpan() {
					kv.Value = attribute.StringValue(unresolvedValue(kv.Value.AsString(), unresolved.Outgoing))
				} else {
					kv.Value = attribute.StringValue(unresolvedValue(kv.Value.AsString(), unresolved.Generic))
				}
				return kv
			}, true
		case attr.ClientNamespace:
			return func(s *Span) attribute.KeyValue {
				kv := getter(s)
				if !s.IsClientSpan() {
					currentNs := kv.Value.AsString()
					if currentNs == "" && willReplaceIP(SpanPeer(s), unresolved.Incoming) {
						kv.Value = attribute.StringValue(s.Service.UID.Namespace)
					}
				}
				return kv
			}, true
		case attr.ServerNamespace:
			return func(s *Span) attribute.KeyValue {
				kv := getter(s)
				if s.IsClientSpan() {
					currentNs := kv.Value.AsString()
					if currentNs == "" && willReplaceIP(SpanHost(s), unresolved.Outgoing) {
						kv.Value = attribute.StringValue(s.Service.UID.Namespace)
					}
				}
				return kv
			}, true
		}

		return getter, ok
	}
}

// promUnresolvedHostGetters wraps spanPromGetters but replacing client and server address
// unresolved metrics by a user-provided tag (usually "unresolved")
func promUnresolvedHostGetters(unresolved UnresolvedNames) func(name attr.Name) (attributes.Getter[*Span, string], bool) {
	return func(name attr.Name) (attributes.Getter[*Span, string], bool) {
		getter := spanPromGetters(name)
		switch name {
		case attr.Client, attr.ClientAddr:
			return func(span *Span) string {
				val := getter(span)
				if span.IsClientSpan() {
					return unresolvedValue(val, unresolved.Generic)
				}
				return unresolvedValue(val, unresolved.Incoming)
			}, true
		case attr.Server, attr.ServerAddr:
			return func(span *Span) string {
				val := getter(span)
				if span.IsClientSpan() {
					return unresolvedValue(val, unresolved.Outgoing)
				}
				return unresolvedValue(val, unresolved.Generic)
			}, true
		case attr.ClientNamespace:
			return func(span *Span) string {
				val := getter(span)
				if !span.IsClientSpan() {
					if val == "" && willReplaceIP(SpanPeer(span), unresolved.Incoming) {
						return span.Service.UID.Namespace
					}
				}
				return val
			}, true
		case attr.ServerNamespace:
			return func(span *Span) string {
				val := getter(span)
				if span.IsClientSpan() {
					if val == "" && willReplaceIP(SpanHost(span), unresolved.Outgoing) {
						return span.Service.UID.Namespace
					}
				}
				return val
			}, true
		}
		return getter, true
	}
}
