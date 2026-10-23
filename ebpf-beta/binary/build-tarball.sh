#!/usr/bin/env bash
# Builds the standalone ("binary") beta tarball from an RPM built by make package-rpm,
# so both carry exactly the same files:
#   <name>/payload.tar.gz   the RPM payload, owned by root
#   <name>/install.sh, uninstall.sh
#   ebpf-beta/binary/build-tarball.sh <rpm> <out.tar.gz>
set -euo pipefail
rpm=$(realpath "$1"); out=$(realpath -m "$2"); here=$(cd "$(dirname "$0")" && pwd)
name=amazon-cloudwatch-agent-ebpf-beta
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
mkdir -p "$work/root" "$work/$name" "$(dirname "$out")"
(cd "$work/root" && rpm2cpio "$rpm" | cpio -idm --quiet)
# Only the agent's own entries: the agent tree, plus files and symlinks elsewhere
# (unit files, /usr/bin and /etc/amazon links). No entries for system directories
# such as / or /usr, whose mode the extraction would otherwise reset, and no
# /usr/lib/.build-id links.
(cd "$work/root" && { echo opt/aws/amazon-cloudwatch-agent; find . ! -type d ! -path './opt/aws/amazon-cloudwatch-agent/*' ! -path './usr/lib/.build-id/*' | sed 's|^\./||'; }) > "$work/files"
tar -C "$work/root" --owner=0 --group=0 --numeric-owner -czf "$work/$name/payload.tar.gz" -T "$work/files"
install -m 0755 "$here/install.sh" "$here/uninstall.sh" "$work/$name/"
tar -C "$work" --owner=0 --group=0 --numeric-owner -czf "$out" "$name"
echo "$out"
