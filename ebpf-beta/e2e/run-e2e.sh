#!/usr/bin/env bash
# Runtime e2e for the OBI receiver inside a test CloudWatch Agent build.
# Linux only (dev desktop or a GitHub ubuntu runner). Needs sudo (only to launch the agent with
# an explicit capability set and to mount a cgroup2 hierarchy on cgroup-v1 hosts).
#
# usage: run-e2e.sh <agent-binary> <scenario> <outdir>
#   scenario tcp     : context_propagation=tcp_service_name, caps = the 8 eBPF caps of the shipped set incl. CAP_NET_ADMIN
#                      and CAP_SYS_ADMIN (SYS_ADMIN is for the FIONREAD compensation that
#                      kernels 6.6.128+/6.12.75+/6.18.14+/6.19+ need; without it OBI disables
#                      context propagation there and the peer.service.name check fails)
#   scenario nonetadmin : context_propagation=disabled, caps = 6 caps (no CAP_NET_ADMIN, no
#                      CAP_SYS_ADMIN): the minimal set when propagation is off
# The agent always runs as the invoking (non-root) user with ambient caps only.
# SKIP_CGROUP2_MOUNT=1 runs "tcp" without preparing cgroup2 (shows the cgroup-v1 degradation).
set -uo pipefail
AGENT=${1:?agent binary}; SCEN=${2:?scenario}; OUT=${3:?outdir}
HERE=$(cd "$(dirname "$0")" && pwd)
FRONTEND_PORT=${FRONTEND_PORT:-18080}; BACKEND_PORT=${BACKEND_PORT:-18081}
REQS=${REQS:-300}
CAPS_BASE="cap_bpf,cap_perfmon,cap_sys_ptrace,cap_dac_read_search,cap_checkpoint_restore,cap_net_raw"
case $SCEN in
  tcp)        CP=tcp_service_name; CAPS="$CAPS_BASE,cap_net_admin,cap_sys_admin"; EXPECT_PEER="--expect-peer-service e2e-frontend=e2e-backend";;
  nonetadmin) CP=disabled;         CAPS="$CAPS_BASE";               EXPECT_PEER="";;
  *) echo "unknown scenario $SCEN"; exit 2;;
esac
rm -rf "$OUT"; mkdir -p "$OUT/agent"
PIDS=()
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done
  sudo pkill -f "$OUT/agent/otel.yaml" 2>/dev/null
  # wait for the agent to exit: the next scenario binds the same ports (e.g. 8888)
  for _ in $(seq 1 30); do pgrep -f "$OUT/agent/otel.yaml" >/dev/null || break; sleep 1; done
}
trap cleanup EXIT

# --- cgroup2: OBI's TCP-option propagation attaches sockops to a cgroup2 root.
# On cgroup-v1 hosts (e.g. Amazon Linux 2) OBI falls back to fsmount(), which
# needs CAP_SYS_ADMIN; since we never grant it, pre-mount the hybrid hierarchy
# (what systemd's "hybrid" mode does) and record that we did so for cleanup.
# /sys/fs/cgroup is a read-only tmpfs there, so it is briefly remounted rw to
# create the mountpoint. ebpf-beta/ci/run.sh undoes this (cgroup2-undo.sh).
if [[ $CP != disabled && ${SKIP_CGROUP2_MOUNT:-0} != 1 ]] && [[ $(stat -fc %T /sys/fs/cgroup) != cgroup2fs ]] \
   && ! mountpoint -q /sys/fs/cgroup/unified; then
  sudo mount -o remount,rw /sys/fs/cgroup && sudo mkdir -p /sys/fs/cgroup/unified \
    && sudo mount -o remount,ro /sys/fs/cgroup \
    && sudo mount -t cgroup2 none /sys/fs/cgroup/unified \
    && echo "mounted cgroup2 at /sys/fs/cgroup/unified" | tee "$OUT/cgroup2-mounted-by-e2e"
fi

# No-leak check: a random secret goes into the query string, request headers and the
# backend's response body; it must not appear anywhere in the agent's output
# (exported metrics and spans, and its logs).
SECRET="e2esecret$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
E2E_SECRET=$SECRET python3 "$HERE/app.py" backend "$BACKEND_PORT" & PIDS+=($!)
python3 "$HERE/app.py" frontend "$FRONTEND_PORT" "http://127.0.0.1:$BACKEND_PORT" & PIDS+=($!)
sleep 1

sed -e "s/@FRONTEND_PORT@/$FRONTEND_PORT/" -e "s/@BACKEND_PORT@/$BACKEND_PORT/" \
    -e "s/@CP@/$CP/" \
    "$HERE/otel-config.tmpl.yaml" > "$OUT/agent/otel.yaml"
: > "$OUT/agent/empty.toml"

ME=$(id -un)
echo "### starting agent as $ME with ambient caps: $CAPS (context_propagation=$CP)"
# capsh: keep the 6/7 caps permitted+inheritable, switch to $ME (keep=1), raise them
# as ambient so they survive the exec of the (non-setuid, no file caps) agent.
sudo /usr/sbin/capsh --caps="$CAPS+eip cap_setpcap,cap_setuid,cap_setgid+ep" --keep=1 \
  --user="$ME" --addamb="$CAPS" -- -c \
  "exec '$AGENT' -config '$OUT/agent/empty.toml' -otelconfig '$OUT/agent/otel.yaml'" \
  > "$OUT/agent/agent.log" 2>&1 &
sleep 3
APID=$(pgrep -f -n "$OUT/agent/otel.yaml" || true)
if [[ -z $APID ]]; then echo "FAIL: agent did not start"; tail -50 "$OUT/agent/agent.log"; exit 1; fi
grep -E '^(Uid|Cap(Inh|Prm|Eff|Bnd|Amb))' /proc/$APID/status | tee "$OUT/agent/proc-status.txt"
EFF=$(awk '/^CapEff/{print $2}' /proc/$APID/status)
/usr/sbin/capsh --decode=$EFF | tee "$OUT/agent/capeff-decoded.txt"
if [[ $SCEN == nonetadmin ]] && /usr/sbin/capsh --decode=$EFF | grep -q cap_sys_admin; then echo "FAIL: agent has CAP_SYS_ADMIN"; exit 1; fi
if [[ $(awk '/^Uid/{print $2}' /proc/$APID/status) == 0 ]]; then echo "FAIL: agent runs as uid 0"; exit 1; fi

# Wait for OBI to attach its probes before generating traffic.
for i in $(seq 1 60); do
  grep -qE 'instrumenting process|new process|Instrumenting' "$OUT/agent/agent.log" && break; sleep 2
done
sleep 5
echo "### traffic: $REQS requests to frontend (-> backend)"
ok=0; for i in $(seq 1 "$REQS"); do
  c=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $SECRET" -H "X-E2E-Secret: $SECRET" \
    "http://127.0.0.1:$FRONTEND_PORT/item/$((i % 5))?token=$SECRET") && [[ $c == 200 ]] && ok=$((ok+1))
  sleep 0.02
done
echo "curl 200s: $ok/$REQS" | tee "$OUT/traffic.txt"
echo "### waiting for metric export"; sleep 20
kill -0 "$APID" 2>/dev/null && echo "agent still running (pid $APID)" || { echo "FAIL: agent died"; tail -50 "$OUT/agent/agent.log"; exit 1; }
grep -iE 'error|warn' "$OUT/agent/agent.log" | grep -viE 'level=info' | head -40 > "$OUT/agent/agent-warn-errors.txt"
python3 "$HERE/verify.py" "$OUT/agent/agent.log" $EXPECT_PEER | tee "$OUT/verify.txt"
rc=${PIPESTATUS[0]}
leaks=$(grep -c "$SECRET" "$OUT/agent/agent.log")
if [[ $leaks -ne 0 ]]; then echo "FAIL: captured request/response data in agent output ($leaks lines)" | tee -a "$OUT/verify.txt"; rc=1
else echo "no-leak: secret from query string, headers and response body not in agent output" | tee -a "$OUT/verify.txt"; fi
exit "$rc"
