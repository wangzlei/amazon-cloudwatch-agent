// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package tpinjector // import "go.opentelemetry.io/obi/pkg/internal/ebpf/tpinjector"

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"go.opentelemetry.io/obi/pkg/kube"
	"go.opentelemetry.io/obi/pkg/obi"
)

// In Kubernetes a client connected to a Service sees the ClusterIP as its peer (the
// DNAT to a pod happens later), and the Service CIDR is usually outside the VPC. The
// cluster's Service addresses are therefore also allowed tcp_service_name peers: the
// ServiceCIDR objects (networking.k8s.io/v1, Kubernetes 1.33+), or else every
// Service's ClusterIPs.

const (
	// kubePeerSyncTimeout bounds how long startup (before sockops is attached) waits for
	// the initial list; the trie is still filled when the list completes later.
	kubePeerSyncTimeout = 10 * time.Second

	// svcNamePeerCIDRsMaxEntries replaces the BPF default (256) so the trie also holds
	// one entry per ClusterIP on clusters without ServiceCIDR objects. The trie is not
	// preallocated: unused entries cost no memory.
	svcNamePeerCIDRsMaxEntries = 65536

	serviceCIDRResource = "servicecidrs"
)

// kubePeerCIDRs keeps the Kubernetes entries of svc_name_peer_cidrs. Prefixes are
// reference counted, because an update adds the new object's prefixes before it
// removes the old ones, and static prefixes (VPC, interfaces, loopback) are never removed.
type kubePeerCIDRs struct {
	log    *slog.Logger
	peers  *ebpf.Map
	static map[netip.Prefix]bool

	mu   sync.Mutex
	refs map[netip.Prefix]int
}

func newKubePeerCIDRs(log *slog.Logger, peers *ebpf.Map, static []netip.Prefix) *kubePeerCIDRs {
	k := &kubePeerCIDRs{log: log, peers: peers, static: map[netip.Prefix]bool{}, refs: map[netip.Prefix]int{}}
	for _, c := range static {
		k.static[c] = true
	}
	return k
}

func (k *kubePeerCIDRs) add(prefixes []netip.Prefix) {
	k.mu.Lock()
	defer k.mu.Unlock()

	for _, c := range prefixes {
		k.refs[c]++
		if k.refs[c] > 1 || k.static[c] {
			continue
		}
		key := svcNamePeerKey(c)
		if err := k.peers.Update(&key, uint8(1), ebpf.UpdateAny); err != nil {
			k.log.Warn("cannot allow tcp_service_name peers", "cidr", c, "error", err)
		}
	}
}

func (k *kubePeerCIDRs) remove(prefixes []netip.Prefix) {
	k.mu.Lock()
	defer k.mu.Unlock()

	for _, c := range prefixes {
		if k.refs[c] == 0 {
			continue
		}
		k.refs[c]--
		if k.refs[c] > 0 || k.static[c] {
			continue
		}
		delete(k.refs, c)
		key := svcNamePeerKey(c)
		if err := k.peers.Delete(&key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			k.log.Debug("cannot remove tcp_service_name peers", "cidr", c, "error", err)
		}
	}
}

// watch fills the trie from the cluster and keeps it current until ctx is done.
// synced is closed once the initial list is in the trie.
func (k *kubePeerCIDRs) watch(ctx context.Context, client kubernetes.Interface, synced chan<- struct{}) {
	defer close(synced)

	factory := informers.NewSharedInformerFactory(client, 0)

	var informer cache.SharedIndexInformer
	var source string
	if hasServiceCIDRs(ctx, client) {
		informer = factory.Networking().V1().ServiceCIDRs().Informer()
		source = "ServiceCIDR objects"
	} else {
		informer = factory.Core().V1().Services().Informer()
		source = "Service ClusterIPs"
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { k.add(kubePeerPrefixes(obj)) },
		UpdateFunc: func(oldObj, newObj any) {
			k.add(kubePeerPrefixes(newObj))
			k.remove(kubePeerPrefixes(oldObj))
		},
		DeleteFunc: func(obj any) { k.remove(kubePeerPrefixes(obj)) },
	})
	if err != nil {
		k.log.Warn("cannot watch Kubernetes Service addresses for tcp_service_name", "error", err)
		return
	}

	factory.Start(ctx.Done())
	if cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		k.log.Info("tcp_service_name is also exchanged with Kubernetes Services", "source", source, "entries", k.size())
	}
}

func (k *kubePeerCIDRs) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.refs)
}

// hasServiceCIDRs reports whether the API server serves networking.k8s.io/v1
// ServiceCIDRs and this agent may list them.
func hasServiceCIDRs(ctx context.Context, client kubernetes.Interface) bool {
	resources, err := client.Discovery().ServerResourcesForGroupVersion(networkingv1.SchemeGroupVersion.String())
	if err != nil {
		return false
	}
	found := false
	for _, r := range resources.APIResources {
		if r.Name == serviceCIDRResource {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	_, err = client.NetworkingV1().ServiceCIDRs().List(ctx, metav1.ListOptions{Limit: 1})
	return err == nil
}

// kubePeerPrefixes returns the allowed peer prefixes of a ServiceCIDR or a Service.
func kubePeerPrefixes(obj any) []netip.Prefix {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	var prefixes []netip.Prefix
	switch o := obj.(type) {
	case *networkingv1.ServiceCIDR:
		for _, c := range o.Spec.CIDRs {
			if p, err := netip.ParsePrefix(c); err == nil {
				prefixes = append(prefixes, p.Masked())
			}
		}
	case *corev1.Service:
		for _, ip := range o.Spec.ClusterIPs {
			if a, err := netip.ParseAddr(ip); err == nil {
				prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	return prefixes
}

// kubeClient returns a client when the Kubernetes metadata decorator is (or detects
// to be) enabled, nil otherwise.
func kubeClient(cfg *obi.Config) kubernetes.Interface {
	k8s := cfg.Attributes.Kubernetes
	mp := kube.NewMetadataProvider(kube.MetadataConfig{Enable: k8s.Enable, KubeConfigPath: k8s.KubeconfigPath}, nil)
	if !mp.IsKubeEnabled() {
		return nil
	}
	client, err := mp.KubeClient()
	if err != nil {
		return nil
	}
	return client
}

// svcNamePeerKey is the svc_name_peer_cidrs trie key of a prefix.
func svcNamePeerKey(c netip.Prefix) BpfSvcNamePeerCidrKeyT {
	key := BpfSvcNamePeerCidrKeyT{Prefixlen: uint32(c.Bits()), Addr: c.Addr().As16()}
	if c.Addr().Is4() {
		key.Prefixlen += ipv4MappedPrefixBits
	}
	return key
}

// stopFunc adapts a context cancel function to the tracer's closers.
type stopFunc context.CancelFunc

func (f stopFunc) Close() error {
	f()
	return nil
}
