#!/usr/bin/env bash
# Static checks of the shipped shell scripts (cheap; no build): syntax with the shell each one
# runs under (sh for #!/bin/sh, else bash), then shellcheck (severity warning and above) on the
# Distributor package scripts. shellcheck is required when CI is set (GitHub Actions), optional
# locally.
#   ebpf-beta/ci/check-scripts.sh
set -euo pipefail
cd "$(dirname "$0")/.."
SYNTAX=(distributor/*.sh binary/*.sh ci/*.sh e2e/*.sh)
SC=(distributor/*.sh)
for f in "${SYNTAX[@]}"; do
  case $(head -1 "$f") in '#!/bin/sh'*) sh -n "$f";; *) bash -n "$f";; esac
done
echo "syntax ok: ${SYNTAX[*]}"
if command -v shellcheck >/dev/null; then
  shellcheck --version | sed -n 2p
  shellcheck -S warning "${SC[@]}"
  echo "shellcheck ok: ${SC[*]}"
elif [[ -n ${CI:-} ]]; then
  echo "shellcheck not installed"; exit 1
else
  echo "shellcheck not installed: skipped"
fi
