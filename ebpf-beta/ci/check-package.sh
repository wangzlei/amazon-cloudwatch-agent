#!/usr/bin/env bash
# Package checks for threat model E3: the systemd drop-in template, which root copies
# into /etc/systemd/system (ctl -a enable-ebpf, %posttrans, install.sh), must not be
# writable by the agent's cwagent user.
#   - RPM: the template is owned by root:root with mode 0644
#   - it is not under any directory the drop-in's ExecStartPre chowns to cwagent
#   - tarball (if given): every payload entry is owned by 0:0, the template has mode 0644
#   ebpf-beta/ci/check-package.sh <rpm> [<tarball>]
set -euo pipefail
rpm=$1; tarball=${2:-}
A=/opt/aws/amazon-cloudwatch-agent; T=$A/share/ebpf/systemd-dropin.conf
fail() { echo "FAIL: $*"; exit 1; }

attrs=$(rpm -qp --qf '[%{FILEMODES:perms} %{FILEUSERNAME}:%{FILEGROUPNAME} %{FILENAMES}\n]' "$rpm" | awk -v t="$T" '$3 == t')
[[ -n $attrs ]] || fail "$T not in $rpm"
[[ $attrs == "-rw-r--r-- root:root $T" ]] || fail "template attributes: $attrs"

dropin=$(rpm2cpio "$rpm" | cpio -i --quiet --to-stdout ".$T")
chowned=$(sed -n 's/^ExecStartPre=[-+@!:]*\/bin\/chown -R cwagent:cwagent //p' <<<"$dropin")
[[ -n $chowned ]] || fail "no chown in the drop-in (check the pattern)"
for d in $chowned; do
  [[ $T == "$d"/* ]] && fail "template $T is under $d, which the drop-in chowns to cwagent"
done

if [[ -n $tarball ]]; then
  payload=$(tar -xzOf "$tarball" --wildcards '*/payload.tar.gz' | tar -tzv)
  bad=$(awk '$2 != "0/0"' <<<"$payload")
  [[ -z $bad ]] || fail "tarball entries not owned by 0/0: $bad"
  grep -qE "^-rw-r--r-- 0/0 .* ${T#/}\$" <<<"$payload" || fail "tarball template is not -rw-r--r-- 0/0"
fi
echo "ok: $T root:root 0644, not under chowned dirs ($(echo $chowned))${tarball:+; tarball payload owned by 0/0}"
