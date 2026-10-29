#!/usr/bin/env bash
# Package checks for threat model E3: everything the privileged eBPF unit runs or reads
# must not be writable by the unprivileged cwagent user, or cwagent could change what a
# process with the eBPF capabilities does:
#   - the eBPF binary, OBI's config (ebpf/obi.yaml) and the systemd unit are root:root and
#     not group/other writable;
#   - none is under the dirs start-amazon-cloudwatch-agent chowns to the agent's
#     run_as_user (etc/, logs/, var/);
#   - tarball (if given): every payload entry is owned by 0:0.
#   ebpf-beta/ci/check-package.sh <rpm> [<tarball>]
set -euo pipefail
rpm=$1; tarball=${2:-}
A=/opt/aws/amazon-cloudwatch-agent
FILES=("$A/bin/amazon-cloudwatch-agent-ebpf" "$A/ebpf/obi.yaml" /etc/systemd/system/amazon-cloudwatch-agent-ebpf.service)
CHOWNED=("$A/etc" "$A/logs" "$A/var")
fail() { echo "FAIL: $*"; exit 1; }

list=$(rpm -qp --qf '[%{FILEMODES:perms} %{FILEUSERNAME}:%{FILEGROUPNAME} %{FILENAMES}\n]' "$rpm")
for f in "${FILES[@]}"; do
  attrs=$(awk -v f="$f" '$3 == f' <<<"$list")
  [[ -n $attrs ]] || fail "$f not in $rpm"
  read -r mode owner _ <<<"$attrs"
  [[ $owner == root:root ]] || fail "$f owned by $owner"
  [[ ${mode:5:1} != w && ${mode:8:1} != w ]] || fail "$f is group/other writable: $mode"
  for d in "${CHOWNED[@]}"; do [[ $f == "$d"/* ]] && fail "$f is under $d, which start-amazon-cloudwatch-agent chowns"; done
done
unit=$(rpm2cpio "$rpm" | cpio -i --quiet --to-stdout ".${FILES[2]}")
grep -q "^ExecStart=${FILES[0]} -config ${FILES[1]}\$" <<<"$unit" || fail "the unit does not run ${FILES[0]} -config ${FILES[1]}"

if [[ -n $tarball ]]; then
  payload=$(tar -xzOf "$tarball" --wildcards '*/payload.tar.gz' | tar -tzv)
  bad=$(awk '$2 != "0/0"' <<<"$payload")
  [[ -z $bad ]] || fail "tarball entries not owned by 0/0: $bad"
fi
echo "ok: ${FILES[*]} root:root, not group/other writable, not under ${CHOWNED[*]}${tarball:+; tarball payload owned by 0/0}"
