#!/usr/bin/env bash
# ebpf-beta checks: the same commands run in .github/workflows/ebpf-beta.yml and locally
# (Linux x86_64 with docker, go, sudo). Every stage first generates OBI's BPF bindings
# (make ebpf-generate: incremental, a no-op when they are current).
#
#   ebpf-beta/ci/run.sh [stage ...]    stages: gates e2e rpm   (default: gates e2e)
#
#   gates  go.mod tidy, dependency-version gate vs CloudWatch Agent v1.300074.0, unit tests
#          (obi receiver, default components, RPM eBPF config, patched OBI packages),
#          builds for linux/amd64 (kept for e2e), linux/arm64 and windows/amd64
#   e2e    runs the agent with the shipped capability set (scenario tcp) and without
#          CAP_NET_ADMIN/CAP_SYS_ADMIN (scenario nonetadmin); asserts RED metrics, spans
#          and peer.service.name from the debug exporter output
#   rpm    make package-rpm (amd64 + arm64), checks the version and the etc/ebpf payload
#
# Env: VERSION (rpm; default 1.300074.0-ebpfbeta.<sha7>), BUILD_SPACE (default build/ebpf-beta),
#      EBPF_OCI_BIN (default docker), GOPROXY/GOSUMDB (go defaults)
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd); cd "$ROOT"
CI=ebpf-beta/ci; LOGS=$ROOT/build/ebpf-beta-logs; mkdir -p "$LOGS"
BS=${BUILD_SPACE:-$ROOT/build/ebpf-beta}; AGENT=$BS/bin/linux_amd64/amazon-cloudwatch-agent
VERSION=${VERSION:-1.300074.0-ebpfbeta.$(git rev-parse --short=7 HEAD)}
STAGES=("$@"); [[ ${#STAGES[@]} -gt 0 ]] || STAGES=(gates e2e)
declare -A RESULT
run() { # run <stage> <cmd...>: log to $LOGS/<stage>.log, record PASS/FAIL
  local s=$1; shift
  echo "=== [$s] $*" | tee -a "$LOGS/$s.log"
  if ( "$@" ) >> "$LOGS/$s.log" 2>&1; then echo "    ok"; else RESULT[$s]=FAIL; echo "    FAILED (see $LOGS/$s.log)"; tail -30 "$LOGS/$s.log"; return 1; fi
}
want() { [[ " ${STAGES[*]} " == *" $1 "* ]]; }
gen() { run "$1" make ebpf-generate EBPF_OCI_BIN="${EBPF_OCI_BIN:-docker}"; }

if want gates; then : > "$LOGS/gates.log"; RESULT[gates]=PASS
  gen gates
  run gates bash -c "cp go.mod /tmp/ebpf-beta.go.mod && cp go.sum /tmp/ebpf-beta.go.sum && GOFLAGS=-mod=mod go mod tidy && diff /tmp/ebpf-beta.go.mod go.mod && diff -q /tmp/ebpf-beta.go.sum go.sum && echo 'go.mod/go.sum are tidy'"
  run gates "$CI/check-versions.sh" "$CI/base-v1.300074.0.go.mod" go.mod
  run gates bash -c "go list -m all > '$LOGS/modlist.txt' && '$CI/check-versions.sh' --full '$CI/base-v1.300074.0.modlist' '$LOGS/modlist.txt'"
  run gates go vet ./receiver/obireceiver/ ./service/defaultcomponents/ ./ebpf-beta/configtest/
  run gates go test -count=1 ./receiver/obireceiver/ ./service/defaultcomponents/ ./ebpf-beta/configtest/
  # The OBI packages the adot-obi patches touch (same list as adot-obi ci/run.sh).
  # tpinjector TestTracer_Constants needs bpf_iter, which OBI only loads on kernels >= 5.11.
  SKIP=""; kv=$(uname -r); kmaj=${kv%%.*}; kmin=${kv#*.}; kmin=${kmin%%.*}
  (( kmaj < 5 || (kmaj == 5 && kmin < 11) )) && SKIP="-skip ^TestTracer_Constants\$"
  run gates bash -c "cd third_party/obi && go test -count=1 $SKIP ./pkg/ebpf/common/... ./pkg/internal/ebpf/tpinjector/... ./pkg/internal/ebpf/gotracer/... \
    ./pkg/export/otel/tracesgen/... ./pkg/export/attributes/... ./pkg/appolly/app/request/... ./pkg/appolly/meta/... \
    ./pkg/appolly/discover/... ./pkg/internal/processcontext/... ./pkg/internal/procname/... ./pkg/internal/jvmtools/... ./pkg/obi/..."
  run gates bash -c "mkdir -p '$(dirname "$AGENT")' && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w -X github.com/aws/amazon-cloudwatch-agent/cfg/agentinfo.VersionStr=$VERSION' -o '$AGENT' ./cmd/amazon-cloudwatch-agent && '$AGENT' -version"
  run gates bash -c "CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/amazon-cloudwatch-agent && echo linux/arm64 ok"
  run gates bash -c "GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/amazon-cloudwatch-agent && echo windows/amd64 ok"
fi

if want e2e; then : > "$LOGS/e2e.log"; RESULT[e2e]=PASS
  [[ -x $AGENT ]] || { gen e2e; run e2e bash -c "mkdir -p '$(dirname "$AGENT")' && CGO_ENABLED=0 go build -trimpath -o '$AGENT' ./cmd/amazon-cloudwatch-agent"; }
  run e2e ebpf-beta/e2e/run-e2e.sh "$AGENT" tcp "$LOGS/e2e-tcp"
  run e2e ebpf-beta/e2e/run-e2e.sh "$AGENT" nonetadmin "$LOGS/e2e-nonetadmin"
  [[ -e $LOGS/e2e-tcp/cgroup2-mounted-by-e2e ]] && ebpf-beta/e2e/cgroup2-undo.sh | tee -a "$LOGS/e2e.log"
fi

if want rpm; then : > "$LOGS/rpm.log"; RESULT[rpm]=PASS
  RPM_VERSION=${VERSION//-/+}   # Tools/src/create_rpm.sh turns '-' into '+'
  # BUILD_SPACE/bin must exist before the parallel make (copy-version-file races mkdir).
  run rpm bash -c "rm -rf '$BS/private' && mkdir -p '$BS/bin' && make package-rpm BUILD_SPACE='$BS' VERSION='$VERSION' EBPF_OCI_BIN='${EBPF_OCI_BIN:-docker}'"
  for a in amd64 arm64; do
    run rpm bash -c "f='$BS/bin/linux/$a/amazon-cloudwatch-agent.rpm'; v=\$(rpm -qp --qf '%{VERSION}' \"\$f\"); [ \"\$v\" = '$RPM_VERSION' ] || { echo \"\$f: version \$v, expected $RPM_VERSION\"; exit 1; }
      l=\$(rpm2cpio \"\$f\" | cpio -t --quiet); for p in etc/ebpf/obi.yaml etc/ebpf/systemd-dropin.conf; do grep -q \"/opt/aws/amazon-cloudwatch-agent/\$p\\\$\" <<<\"\$l\" || { echo \"\$f: \$p missing\"; exit 1; }; done
      echo \"$a: \$v \$(du -h \"\$f\" | cut -f1), has etc/ebpf/obi.yaml + systemd-dropin.conf\""
  done
fi

echo; echo "===== summary ====="
for s in gates e2e rpm; do [[ -n ${RESULT[$s]:-} ]] && printf '%-6s %s\n' "$s" "${RESULT[$s]}"; done | tee "$LOGS/summary.txt"
! grep -q FAIL "$LOGS/summary.txt"
