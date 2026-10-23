#!/usr/bin/env bash
# ebpf-beta checks: the same commands run in .github/workflows/ebpf-beta.yml and locally
# (Linux x86_64 with docker, go, sudo). Every stage first generates OBI's BPF bindings
# (make ebpf-generate: incremental, a no-op when they are current).
#
#   ebpf-beta/ci/run.sh [stage ...]    stages: deps unit cross e2e rpm image   (default: gates e2e)
#                                     gates = deps unit cross
#
#   deps   go.mod tidy, dependency-version gate vs CloudWatch Agent v1.300074.0, go vet
#   unit   unit tests: obi receiver, default components, RPM eBPF config, patched OBI packages
#   cross  builds for linux/amd64 (kept for e2e), linux/arm64 and windows/amd64
#   e2e    runs the agent with the shipped capability set (scenario tcp) and without
#          CAP_NET_ADMIN/CAP_SYS_ADMIN (scenario nonetadmin); asserts RED metrics, spans
#          and peer.service.name from the debug exporter output
#   rpm    make package-rpm (amd64 + arm64), checks the version and the etc/ebpf payload
#   image  official-layout image (ebpf-beta/image/Dockerfile): builds linux/amd64 into the
#          local docker and smoke-tests it; IMAGE_PUSH=1 then pushes linux/amd64+arm64
#
# Env: VERSION (rpm; default 1.300074.0-ebpfbeta.<sha7>), BUILD_SPACE (default build/ebpf-beta),
#      EBPF_OCI_BIN (default docker), GOPROXY/GOSUMDB (go defaults)
#      IMAGE (default cloudwatch-agent-ebpf-beta), IMAGE_TAGS (space-separated, default $VERSION),
#      IMAGE_PUSH=1 (image: push the multi-arch image; needs docker login)
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd); cd "$ROOT"
CI=ebpf-beta/ci; LOGS=$ROOT/build/ebpf-beta-logs; mkdir -p "$LOGS"
BS=${BUILD_SPACE:-$ROOT/build/ebpf-beta}; AGENT=$BS/bin/linux_amd64/amazon-cloudwatch-agent
VERSION=${VERSION:-1.300074.0-ebpfbeta.$(git rev-parse --short=7 HEAD)}
STAGES=("$@"); [[ ${#STAGES[@]} -gt 0 ]] || STAGES=(gates e2e)
[[ " ${STAGES[*]} " == *" gates "* ]] && STAGES+=(deps unit cross)
declare -A RESULT
run() { # run <stage> <cmd...>: log to $LOGS/<stage>.log, record PASS/FAIL
  local s=$1; shift
  echo "=== [$s] $*" | tee -a "$LOGS/$s.log"
  if ( "$@" ) >> "$LOGS/$s.log" 2>&1; then echo "    ok"; else RESULT[$s]=FAIL; echo "    FAILED (see $LOGS/$s.log)"; tail -30 "$LOGS/$s.log"; return 1; fi
}
want() { [[ " ${STAGES[*]} " == *" $1 "* ]]; }
gen() { run "$1" make ebpf-generate EBPF_OCI_BIN="${EBPF_OCI_BIN:-docker}"; }

if want deps; then : > "$LOGS/deps.log"; RESULT[deps]=PASS
  gen deps
  run deps bash -c "cp go.mod /tmp/ebpf-beta.go.mod && cp go.sum /tmp/ebpf-beta.go.sum && GOFLAGS=-mod=mod go mod tidy && diff /tmp/ebpf-beta.go.mod go.mod && diff -q /tmp/ebpf-beta.go.sum go.sum && echo 'go.mod/go.sum are tidy'"
  run deps "$CI/check-versions.sh" "$CI/base-v1.300074.0.go.mod" go.mod
  run deps bash -c "go list -m all > '$LOGS/modlist.txt' && '$CI/check-versions.sh' --full '$CI/base-v1.300074.0.modlist' '$LOGS/modlist.txt'"
  run deps go vet ./receiver/obireceiver/ ./service/defaultcomponents/ ./ebpf-beta/configtest/
fi

if want unit; then : > "$LOGS/unit.log"; RESULT[unit]=PASS
  gen unit
  run unit go test -count=1 ./receiver/obireceiver/ ./service/defaultcomponents/ ./ebpf-beta/configtest/
  # The OBI packages the adot-obi patches touch (same list as adot-obi ci/run.sh).
  # tpinjector TestTracer_Constants needs bpf_iter, which OBI only loads on kernels >= 5.11.
  SKIP=""; kv=$(uname -r); kmaj=${kv%%.*}; kmin=${kv#*.}; kmin=${kmin%%.*}
  (( kmaj < 5 || (kmaj == 5 && kmin < 11) )) && SKIP="-skip ^TestTracer_Constants\$"
  run unit bash -c "cd third_party/obi && go test -count=1 $SKIP ./pkg/ebpf/common/... ./pkg/internal/ebpf/tpinjector/... ./pkg/internal/ebpf/gotracer/... \
    ./pkg/export/otel/tracesgen/... ./pkg/export/attributes/... ./pkg/appolly/app/request/... ./pkg/appolly/meta/... \
    ./pkg/appolly/discover/... ./pkg/internal/processcontext/... ./pkg/internal/procname/... ./pkg/internal/jvmtools/... ./pkg/obi/..."
fi

if want cross; then : > "$LOGS/cross.log"; RESULT[cross]=PASS
  gen cross
  run cross bash -c "mkdir -p '$(dirname "$AGENT")' && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o '$AGENT' ./cmd/amazon-cloudwatch-agent && echo '$VERSION' > '$(dirname "$AGENT")/CWAGENT_VERSION' && '$AGENT' -version"
  run cross bash -c "CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/amazon-cloudwatch-agent && echo linux/arm64 ok"
  run cross bash -c "GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/amazon-cloudwatch-agent && echo windows/amd64 ok"
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
      l=\$(rpm2cpio \"\$f\" | cpio -t --quiet); for p in etc/ebpf/obi.yaml share/ebpf/systemd-dropin.conf; do grep -q \"/opt/aws/amazon-cloudwatch-agent/\$p\\\$\" <<<\"\$l\" || { echo \"\$f: \$p missing\"; exit 1; }; done
      echo \"$a: \$v \$(du -h \"\$f\" | cut -f1), has etc/ebpf/obi.yaml + systemd-dropin.conf\""
  done
fi

if want image; then : > "$LOGS/image.log"; RESULT[image]=PASS
  IBS=$ROOT/build/ebpf-beta-image IMAGE=${IMAGE:-cloudwatch-agent-ebpf-beta}
  read -ra TAGS <<<"${IMAGE_TAGS:-$VERSION}"; TARGS=(); for t in "${TAGS[@]}"; do TARGS+=(-t "$IMAGE:$t"); done
  OBI_SOURCE=$(awk '/^adot-obi:/{print "adot-obi-"$2}' third_party/obi/ADOT_OBI_SOURCE)
  BARGS=(-f ebpf-beta/image/Dockerfile --build-arg VERSION="$VERSION" --build-arg OBI_SOURCE="$OBI_SOURCE")
  run image bash -c "rm -rf '$IBS' && mkdir -p '$IBS/bin' && make build-for-docker-amd64 build-for-docker-arm64 BUILD_SPACE='$IBS' VERSION='$VERSION' EBPF_OCI_BIN='${EBPF_OCI_BIN:-docker}' && cp packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml '$IBS/obi.yaml' \
    && for a in amd64 arm64; do echo '$VERSION' > '$IBS'/bin/linux_\$a/CWAGENT_VERSION; done"  # the agent reads its version from this file next to the binary
  run image docker buildx build --platform linux/amd64 --load "${BARGS[@]}" -t "$IMAGE:smoke" "$IBS"
  # scratch image: no shell; run the agent binary directly and copy the eBPF config out
  run image bash -c "docker run --rm --entrypoint /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent '$IMAGE:smoke' -version | grep -F '$VERSION' \
    && c=\$(docker create '$IMAGE:smoke') && docker cp \$c:/opt/aws/amazon-cloudwatch-agent/etc/ebpf/obi.yaml - | tar -xO | diff - packaging/dependencies/amazon-cloudwatch-agent-ebpf.yaml; rc=\$?; docker rm -f \$c >/dev/null; [ \$rc = 0 ] && echo 'image has etc/ebpf/obi.yaml (== RPM default)'"
  if [[ ${IMAGE_PUSH:-0} == 1 ]]; then
    # multi-platform push needs a docker-container builder (the default docker driver cannot)
    run image bash -c "docker buildx inspect ebpf-beta >/dev/null 2>&1 || docker buildx create --name ebpf-beta --driver docker-container >/dev/null"
    run image docker buildx build --builder ebpf-beta --platform linux/amd64,linux/arm64 --push "${BARGS[@]}" "${TARGS[@]}" "$IBS"
    run image bash -c "for t in ${TAGS[*]}; do docker buildx imagetools inspect '$IMAGE:'\$t | grep -E '^(Name|Digest)|Platform'; done"
  fi
fi

echo; echo "===== summary ====="
for s in deps unit cross e2e rpm image; do [[ -n ${RESULT[$s]:-} ]] && printf '%-6s %s\n' "$s" "${RESULT[$s]}"; done | tee "$LOGS/summary.txt"
! grep -q FAIL "$LOGS/summary.txt"
