// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package tpinjector

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestKubePeerPrefixes(t *testing.T) {
	serviceCIDR := &networkingv1.ServiceCIDR{Spec: networkingv1.ServiceCIDRSpec{CIDRs: []string{"10.100.0.0/16", "fd00:10:96::/112"}}}
	assert.Equal(t, []netip.Prefix{netip.MustParsePrefix("10.100.0.0/16"), netip.MustParsePrefix("fd00:10:96::/112")},
		kubePeerPrefixes(serviceCIDR))

	service := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIPs: []string{"10.100.12.7", "fd00:10:96::a"}}}
	assert.Equal(t, []netip.Prefix{netip.MustParsePrefix("10.100.12.7/32"), netip.MustParsePrefix("fd00:10:96::a/128")},
		kubePeerPrefixes(service))

	headless := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone, ClusterIPs: []string{corev1.ClusterIPNone}}}
	assert.Empty(t, kubePeerPrefixes(headless))

	assert.Equal(t, kubePeerPrefixes(service), kubePeerPrefixes(cache.DeletedFinalStateUnknown{Obj: service.DeepCopy()}))
}

func TestHasServiceCIDRs(t *testing.T) {
	withResources := func(names ...string) *fake.Clientset {
		client := fake.NewClientset(&networkingv1.ServiceCIDR{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes"}})
		var resources []metav1.APIResource
		for _, n := range names {
			resources = append(resources, metav1.APIResource{Name: n})
		}
		client.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{
			{GroupVersion: networkingv1.SchemeGroupVersion.String(), APIResources: resources},
		}
		return client
	}

	assert.True(t, hasServiceCIDRs(context.Background(), withResources("ingresses", serviceCIDRResource)))
	assert.False(t, hasServiceCIDRs(context.Background(), withResources("ingresses", "networkpolicies")))
	assert.False(t, hasServiceCIDRs(context.Background(), fake.NewClientset()))
}

func TestSvcNamePeerKey(t *testing.T) {
	v4 := svcNamePeerKey(netip.MustParsePrefix("10.100.0.0/16"))
	assert.Equal(t, uint32(ipv4MappedPrefixBits+16), v4.Prefixlen)
	assert.Equal(t, netip.MustParseAddr("::ffff:10.100.0.0").As16(), v4.Addr)

	v6 := svcNamePeerKey(netip.MustParsePrefix("fd00:10:96::/112"))
	assert.Equal(t, uint32(112), v6.Prefixlen)
}
