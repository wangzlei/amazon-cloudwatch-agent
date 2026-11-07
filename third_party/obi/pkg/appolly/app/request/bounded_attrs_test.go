// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/appolly/app/svc"
	attr "go.opentelemetry.io/obi/pkg/export/attributes/names"
)

func TestForwardedHostNameValidation(t *testing.T) {
	for in, want := range map[string]string{
		"api.example.com":           "api.example.com",
		" api.example.com:8443 , x": "api.example.com:8443",
		"10.0.0.1:80":               "10.0.0.1:80",
		"[fd00::1]:8080":            "[fd00::1]:8080",
		"my_svc.internal":           "my_svc.internal",
		"":                          "",
		"evil<script>":              "",
		"a b":                       "",
		"a/b":                       "",
		"x\x00y":                    "",
		"é.example":                 "",
		strings.Repeat("a", 260):    "",
	} {
		assert.Equal(t, want, ForwardedHostName(in), "%q", in)
	}
	assert.Equal(t, strings.Repeat("a", 259), ForwardedHostName(strings.Repeat("a", 259)))
}

func peerName(t *testing.T, s *Span) string {
	t.Helper()
	g, ok := spanOTELGetters(attr.PeerServiceName)
	require.True(t, ok)
	kv := g(s)
	if !kv.Valid() {
		return ""
	}
	return kv.Value.AsString()
}

func TestServerPeerServiceNameIsBoundedPerService(t *testing.T) {
	bounded.reset()
	t.Cleanup(bounded.reset)
	a := svc.Attrs{UID: svc.UID{Name: "a", Instance: "1"}}
	b := svc.Attrs{UID: svc.UID{Name: "b", Instance: "2"}}

	distinct := map[string]bool{}
	for i := 0; i < 2*maxDistinctPeerNames; i++ {
		distinct[peerName(t, &Span{Type: EventTypeHTTP, Service: a, ForwardedHost: fmt.Sprintf("h%d.example.com", i)})] = true
	}
	// limit names kept, everything else collapses into one value
	assert.Len(t, distinct, maxDistinctPeerNames+1)
	assert.True(t, distinct[OverflowAttrValue])
	// a name admitted earlier keeps its value
	assert.Equal(t, "h0.example.com", peerName(t, &Span{Type: EventTypeHTTP, Service: a, ForwardedHost: "h0.example.com"}))
	// another service has its own budget
	assert.Equal(t, "h99.example.com", peerName(t, &Span{Type: EventTypeHTTP, Service: b, ForwardedHost: "h99.example.com"}))
	// invalid header: no attribute
	assert.Empty(t, peerName(t, &Span{Type: EventTypeHTTP, Service: b, ForwardedHost: "<x>"}))
	// client side (from the TCP option, not a header) is not affected
	assert.Equal(t, "payment", peerName(t, &Span{Type: EventTypeHTTPClient, Service: a, PeerServiceName: "payment"}))
}

func TestAWSResourceAttributesAreValidatedAndBounded(t *testing.T) {
	bounded.reset()
	t.Cleanup(bounded.reset)
	g, ok := spanOTELGetters(attr.AWSS3Bucket)
	require.True(t, ok)
	s3 := func(service svc.Attrs, bucket string) string {
		kv := g(&Span{Type: EventTypeHTTPClient, SubType: HTTPSubtypeAWSS3, Service: service, AWS: &AWS{S3: AWSS3{Bucket: bucket}}})
		if !kv.Valid() {
			return ""
		}
		return kv.Value.AsString()
	}
	a := svc.Attrs{UID: svc.UID{Name: "a"}}
	assert.Equal(t, "my-bucket.logs", s3(a, "my-bucket.logs"))
	assert.Empty(t, s3(a, "bad bucket"))
	assert.Empty(t, s3(a, strings.Repeat("b", 300)))
	for i := 0; i < 2*maxDistinctAWSResources; i++ {
		s3(a, fmt.Sprintf("bucket-%d", i))
	}
	assert.Equal(t, OverflowAttrValue, s3(a, "one-more"))
	assert.Equal(t, "my-bucket.logs", s3(a, "my-bucket.logs"))
}

func TestHTTPClientHostIgnoresInvalidHost(t *testing.T) {
	s := &Span{Type: EventTypeHTTPClient, Statement: "http" + SchemeHostSeparator + "api.example.com:8080", Host: "10.0.0.5"}
	assert.Equal(t, "api.example.com:8080", HTTPClientHost(s))
	s.Statement = "http" + SchemeHostSeparator + "bad host<>"
	assert.Equal(t, "10.0.0.5", HTTPClientHost(s))
}
