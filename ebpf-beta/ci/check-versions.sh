#!/usr/bin/env bash
# Gate: adding OBI must not change the version of any module the release
# cwagent go.mod already has, except for bumps explicitly listed (with the
# forcing dependency and the reason) in ebpf-beta/ci/allowed-bumps.txt.
# Net-new (OBI-only) modules are fine. Removals are reported.
#   check-versions.sh <release go.mod> <new go.mod> [allowlist]
#   check-versions.sh --full <release `go list -m all`> <new `go list -m all`> [allowlist]
# --full compares the complete selected module lists (catches modules cwagent
# selects but go.mod prunes, e.g. github.com/cilium/ebpf).
set -euo pipefail
FULL=0; [[ ${1:-} == --full ]] && { FULL=1; shift; }
BASE=${1:?release go.mod}; NEW=${2:?new go.mod}
ALLOW=${3:-"$(dirname "$0")/allowed-bumps.txt"}
reqs() { if [[ $FULL == 1 ]]; then tail -n +2 "$1" | awk 'NF>=2 {print $1, $2}' | LC_ALL=C sort; return; fi
  go mod edit -json "$1" | python3 -c 'import json,sys; [print(r["Path"], r["Version"]) for r in (json.load(sys.stdin).get("Require") or [])]' | LC_ALL=C sort; }
allowed() { grep -v '^\s*#' "$ALLOW" 2>/dev/null | awk -v m="$1" -v o="$2" -v n="$3" '$1==m && $2==o && $4==n {f=1} END{exit !f}'; }
bad=0; changed=0; added=0; removed=0
while read -r p old new; do
  if [[ $old == - ]]; then added=$((added+1)); continue; fi
  if [[ $new == - ]]; then removed=$((removed+1)); echo "removed (tidy): $p $old"; continue; fi
  changed=$((changed+1))
  if allowed "$p" "$old" "$new"; then echo "allowed bump: $p $old -> $new  ($(grep "^$p " "$ALLOW" | cut -d'|' -f2- | xargs))"
  else echo "NOT ALLOWED: $p $old -> $new"; bad=1; fi
done < <(LC_ALL=C join -a1 -a2 -e - -o 0,1.2,2.2 <(reqs "$BASE") <(reqs "$NEW") | awk '$2!=$3')
echo "summary: $changed pre-existing modules changed version (not allowlisted: $bad), $added OBI-only modules added, $removed removed"
exit $bad
