#!/usr/bin/env bash
# Builds the SSM Distributor package files of ADOT-CloudWatchAgent-eBPF-Beta from the two RPMs
# (no AWS access; CI runs it on the RPMs of the rpm job, publish-scripts.sh before uploading):
#
#   ebpf-beta/distributor/build-zips.sh <x86_64.rpm> <aarch64.rpm> <versionName> <outdir>
#
#   <outdir>/amazon-cloudwatch-agent-ebpf-beta_{x86_64,arm64}.zip   each: amazon-cloudwatch-agent.rpm
#                                                                  + install.sh + uninstall.sh
#   <outdir>/manifest.json   manifest.template.json with the version and the zips' sha256
set -euo pipefail
[[ $# == 4 ]] || { sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
X86=$(realpath "$1") ARM=$(realpath "$2") VERSION=$3 OUT=$(realpath -m "$4")
DIST=$(cd "$(dirname "$0")" && pwd)
die() { echo "ERROR: $*" >&2; exit 1; }

# Distributor versionName: no '+' (the RPM version has one; use the '-' form).
[[ $VERSION =~ ^[a-zA-Z0-9_.-]{1,128}$ ]] || die "bad versionName $VERSION"
[[ $(rpm -qp --qf '%{ARCH}' "$X86") == x86_64 ]] || die "$X86 is not x86_64"
[[ $(rpm -qp --qf '%{ARCH}' "$ARM") == aarch64 ]] || die "$ARM is not aarch64"
sh -n "$DIST/install.sh" && sh -n "$DIST/uninstall.sh"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
mkdir -p "$OUT"
for d in x86_64 arm64; do
  mkdir -p "$W/$d"
  if [[ $d == x86_64 ]]; then cp "$X86" "$W/$d/amazon-cloudwatch-agent.rpm"; else cp "$ARM" "$W/$d/amazon-cloudwatch-agent.rpm"; fi
  install -m 0644 "$DIST/install.sh" "$DIST/uninstall.sh" "$W/$d/"
  rm -f "$OUT/amazon-cloudwatch-agent-ebpf-beta_$d.zip"
  (cd "$W/$d" && zip -q -X "$OUT/amazon-cloudwatch-agent-ebpf-beta_$d.zip" amazon-cloudwatch-agent.rpm install.sh uninstall.sh)
  [[ $(unzip -Z1 "$OUT/amazon-cloudwatch-agent-ebpf-beta_$d.zip" | sort | tr '\n' ' ') == "amazon-cloudwatch-agent.rpm install.sh uninstall.sh " ]] ||
    die "unexpected contents of amazon-cloudwatch-agent-ebpf-beta_$d.zip"
done
X=$(sha256sum "$OUT/amazon-cloudwatch-agent-ebpf-beta_x86_64.zip" | cut -d' ' -f1)
A=$(sha256sum "$OUT/amazon-cloudwatch-agent-ebpf-beta_arm64.zip" | cut -d' ' -f1)
jq --arg v "$VERSION" --arg x "$X" --arg a "$A" '
  .version = $v
  | .files["amazon-cloudwatch-agent-ebpf-beta_x86_64.zip"].checksums.sha256 = $x
  | .files["amazon-cloudwatch-agent-ebpf-beta_arm64.zip"].checksums.sha256 = $a' \
  "$DIST/manifest.template.json" > "$OUT/manifest.json"
echo "$OUT: $VERSION"
for d in x86_64 arm64; do echo "  amazon-cloudwatch-agent-ebpf-beta_$d.zip $(du -h "$OUT/amazon-cloudwatch-agent-ebpf-beta_$d.zip" | cut -f1): $(unzip -Z1 "$OUT/amazon-cloudwatch-agent-ebpf-beta_$d.zip" | tr '\n' ' ')"; done
