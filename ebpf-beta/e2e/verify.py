#!/usr/bin/env python3
"""Assert on the debug exporter's detailed text output (agent stdout).

usage: verify.py <agent.log> [--expect-peer-service FROM=TO]
Checks:
  - http.server.request.duration samples for e2e-frontend and e2e-backend
  - http.client.request.duration samples for e2e-frontend
  - (optional) a client datapoint of service FROM carries peer.service.name=TO
  - spans: SERVER spans for both services, CLIENT span(s) for e2e-frontend
Exit code 0 = all checks pass.
"""
import collections
import json
import re
import sys

KV = re.compile(r"^\s*-> ([^:]+): (\w+)\((.*)\)\s*$")
DESC_NAME = re.compile(r"^\s*-> Name: (\S+)\s*$")
KIND = re.compile(r"^Kind\s*:\s*(\w+)")


def main():
    path = sys.argv[1]
    expect_peer = None
    if "--expect-peer-service" in sys.argv:
        expect_peer = tuple(sys.argv[sys.argv.index("--expect-peer-service") + 1].split("="))
    samples = collections.Counter()   # (svc, metric) -> requests (cumulative Count, last export, summed over series)
    last = {}                         # (svc, metric, series attrs) -> last cumulative Count
    dps = collections.Counter()
    peer = collections.Counter()      # (svc, peer.service.name) on client duration
    spans = collections.Counter()     # (svc, kind)
    span_peer = collections.Counter()
    examples = {}
    svc = metric = None
    section = None
    dp_attrs = {}
    span_kind = None
    span_attrs = {}
    for raw in open(path, errors="replace"):
        line = raw.rstrip("\n")
        s = line.strip()
        if s.startswith("ResourceMetrics #") or s.startswith("ResourceSpans #"):
            svc = metric = None; section = None
        elif s == "Resource attributes:":
            section = "resource"
        elif s == "Descriptor:":
            section = "descriptor"
        elif s == "Data point attributes:":
            section = "dp"; dp_attrs = {}
        elif s.startswith("Span #"):
            if span_kind:
                spans[(svc, span_kind)] += 1
                if span_kind == "Client" and span_attrs.get("peer.service.name"):
                    span_peer[(svc, span_attrs["peer.service.name"])] += 1
            span_kind = None; span_attrs = {}; section = "span"
        elif KIND.match(s) and section in ("span", "spanattrs"):
            span_kind = KIND.match(s).group(1).replace("SPAN_KIND_", "").title()
        elif s == "Attributes:" and section in ("span", "spanattrs"):
            section = "spanattrs"
        elif s.startswith("Count:") and metric:
            n = int(s.split(":", 1)[1])
            key = (svc, metric)
            last[(svc, metric, tuple(sorted(dp_attrs.items())))] = n; dps[key] += 1
            examples.setdefault(key, dict(dp_attrs))
            section = None
        elif section == "descriptor" and DESC_NAME.match(line):
            metric = DESC_NAME.match(line).group(1)
        else:
            m = KV.match(line)
            if m:
                k, _, v = m.groups()
                if section == "resource" and k == "service.name":
                    svc = v
                elif section == "descriptor" and k == "Name":
                    metric = v
                elif section == "dp":
                    dp_attrs[k] = v
                elif section == "spanattrs":
                    span_attrs[k] = v
    if span_kind:
        spans[(svc, span_kind)] += 1
        if span_kind == "Client" and span_attrs.get("peer.service.name"):
            span_peer[(svc, span_attrs["peer.service.name"])] += 1

    for (s_, m_, a_), n in last.items():
        samples[(s_, m_)] += n
        if m_ == "http.client.request.duration":
            peer[(s_, dict(a_).get("peer.service.name"))] += n
    print("requests per (service.name, metric) = cumulative histogram Count at last export:")
    for (s_, m_), n in sorted(samples.items(), key=lambda kv: (str(kv[0][0]), kv[0][1])):
        if m_.startswith("http."):
            print("  %-14s %-30s datapoints=%-4d count=%d" % (s_, m_, dps[(s_, m_)], n))
    for k in sorted(examples, key=str):
        if k[1] in ("http.server.request.duration", "http.client.request.duration"):
            print("  example dp attrs %s %s: %s" % (k[0], k[1], json.dumps(examples[k], sort_keys=True)))
    print("http.client.request.duration count by (service.name, peer.service.name):", dict(peer))
    print("spans by (service.name, kind):", dict(spans))
    print("client spans by (service.name, peer.service.name):", dict(span_peer))
    ok = True
    for s_ in ("e2e-frontend", "e2e-backend"):
        if samples[(s_, "http.server.request.duration")] <= 0:
            print("FAIL: no http.server.request.duration samples for", s_); ok = False
    if samples[("e2e-frontend", "http.client.request.duration")] <= 0:
        print("FAIL: no http.client.request.duration samples for e2e-frontend"); ok = False
    if expect_peer and peer[expect_peer] <= 0:
        print("FAIL: no client RED datapoint %s -> peer.service.name=%s" % expect_peer); ok = False
    print("RESULT:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
