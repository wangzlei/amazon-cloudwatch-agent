// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package tpinjector // import "go.opentelemetry.io/obi/pkg/internal/ebpf/tpinjector"

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const (
	imdsBase        = "http://169.254.169.254/latest"
	imdsTimeout     = time.Second
	imdsTokenTTLSec = "60"
)

// privateCIDRs stand in for the VPC when the host has no EC2 instance metadata.
var privateCIDRs = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// loopbackCIDRs never leave the host, so they are always allowed.
var loopbackCIDRs = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// svcNamePeerCIDRs returns the networks a server may send its service name to: the
// VPC CIDRs of the host (or the private ranges when that is not an EC2 instance), the
// networks attached to the host's interfaces (container bridges) and loopback.
func svcNamePeerCIDRs(ctx context.Context) ([]netip.Prefix, string) {
	cidrs, source := vpcCIDRs(ctx)
	cidrs = append(cidrs, interfaceCIDRs()...)
	cidrs = append(cidrs, loopbackCIDRs...)
	return cidrs, source
}

func vpcCIDRs(ctx context.Context) ([]netip.Prefix, string) {
	cidrs, err := imdsVPCCIDRs(ctx)
	if err != nil || len(cidrs) == 0 {
		return append([]netip.Prefix(nil), privateCIDRs...), fmt.Sprintf("private ranges (no VPC CIDRs from instance metadata: %v)", err)
	}
	return cidrs, "instance metadata"
}

func interfaceCIDRs() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	var cidrs []netip.Prefix
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		prefix, err := netip.ParsePrefix(ipNet.String())
		if err != nil || prefix.Addr().IsLinkLocalUnicast() {
			continue
		}
		cidrs = append(cidrs, prefix.Masked())
	}
	return cidrs
}

// imdsVPCCIDRs reads the IPv4 and IPv6 CIDR blocks of the VPC of every network
// interface of the instance (IMDSv2).
func imdsVPCCIDRs(ctx context.Context) ([]netip.Prefix, error) {
	client := &http.Client{Timeout: imdsTimeout}

	token, err := imdsToken(ctx, client)
	if err != nil {
		return nil, err
	}

	macs, err := imdsLines(ctx, client, token, "/meta-data/network/interfaces/macs/")
	if err != nil {
		return nil, err
	}

	var cidrs []netip.Prefix
	for _, mac := range macs {
		mac = strings.TrimSuffix(mac, "/")
		for _, field := range []string{"vpc-ipv4-cidr-blocks", "vpc-ipv6-cidr-blocks"} {
			lines, err := imdsLines(ctx, client, token, "/meta-data/network/interfaces/macs/"+mac+"/"+field)
			if err != nil {
				continue // an interface without IPv6 returns 404
			}
			for _, l := range lines {
				if p, err := netip.ParsePrefix(l); err == nil {
					cidrs = append(cidrs, p.Masked())
				}
			}
		}
	}
	return cidrs, nil
}

func imdsToken(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imdsBase+"/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", imdsTokenTTLSec)

	body, err := imdsDo(client, req)
	if err != nil {
		return "", fmt.Errorf("instance metadata token: %w", err)
	}
	return string(body), nil
}

func imdsLines(ctx context.Context, client *http.Client, token, path string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)

	body, err := imdsDo(client, req)
	if err != nil {
		return nil, err
	}

	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func imdsDo(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: %s", req.Method, req.URL.Path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
