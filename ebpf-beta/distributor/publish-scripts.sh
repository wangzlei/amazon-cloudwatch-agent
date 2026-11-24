#!/usr/bin/env bash
# Publish a NON-default version of the SSM Distributor package ADOT-CloudWatchAgent-eBPF-Beta
# from already-built RPMs and THIS directory's install.sh / uninstall.sh (no build).
#
#   ebpf-beta/distributor/publish-scripts.sh <rpm-dir> <versionName>
#   ebpf-beta/distributor/publish-scripts.sh <x86_64.rpm> <aarch64.rpm> <versionName>
#
# <rpm-dir>: the CI artifact rpm-<version> (gh run download <run> -n rpm-<version> -D <dir>:
# amazon-cloudwatch-agent-<version>.{x86_64,aarch64}.rpm) or a local ci/run.sh rpm output
# build/ebpf-beta/bin/linux ({amd64,arm64}/amazon-cloudwatch-agent.rpm).
# build-zips.sh makes the zips (amazon-cloudwatch-agent-ebpf-beta_{x86_64,arm64}.zip, each with
# amazon-cloudwatch-agent.rpm + install.sh + uninstall.sh) and manifest.json, uploaded to
# s3://$BUCKET/<versionName>/ (must not exist yet), then a new document version
# (update-document --document-version '$LATEST'). The default version and the share are not
# changed. Uses the default AWS credentials (ada, Admin, 292779133546).
set -euo pipefail
usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
DIST=$(cd "$(dirname "$0")" && pwd)
die() { echo "ERROR: $*" >&2; exit 1; }
case $# in
  2) D=$1 VERSION=$2
     X86=$(compgen -G "$D/amazon-cloudwatch-agent-*.x86_64.rpm" || compgen -G "$D/amd64/amazon-cloudwatch-agent.rpm") || die "no x86_64 RPM in $D"
     ARM=$(compgen -G "$D/amazon-cloudwatch-agent-*.aarch64.rpm" || compgen -G "$D/arm64/amazon-cloudwatch-agent.rpm") || die "no aarch64 RPM in $D"
     [[ $X86 != *$'\n'* && $ARM != *$'\n'* ]] || die "more than one RPM per arch in $D";;
  3) X86=$1 ARM=$2 VERSION=$3;;
  *) usage;;
esac
X86=$(realpath "$X86") ARM=$(realpath "$ARM")
PKG=ADOT-CloudWatchAgent-eBPF-Beta REGION=us-west-2 ACCOUNT=292779133546
BUCKET=adot-obi-distributor-292779133546-usw2
S3=s3://$BUCKET/$VERSION
export AWS_REGION=$REGION AWS_PAGER=

[[ $VERSION =~ ^[a-zA-Z0-9_.-]{1,128}$ ]] || die "bad versionName $VERSION"
[[ $(aws sts get-caller-identity --query Account --output text) == "$ACCOUNT" ]] || die "not account $ACCOUNT"
aws ssm list-document-versions --name "$PKG" --query 'DocumentVersions[].VersionName' --output text |
  tr '\t' '\n' | grep -qxF "$VERSION" && die "versionName $VERSION already exists"
[[ -z $(aws s3 ls "$S3/") ]] || die "$S3/ already exists"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
echo "x86_64: $X86"; echo "arm64:  $ARM"
"$DIST/build-zips.sh" "$X86" "$ARM" "$VERSION" "$W"
cat "$W/manifest.json"

for d in x86_64 arm64; do aws s3 cp --only-show-errors "$W/amazon-cloudwatch-agent-ebpf-beta_$d.zip" "$S3/"; done
NEW=$(aws ssm update-document --name "$PKG" --content "file://$W/manifest.json" \
  --attachments "Key=SourceUrl,Values=$S3" --version-name "$VERSION" --document-version '$LATEST' \
  --query DocumentDescription.DocumentVersion --output text)
for _ in $(seq 60); do
  ST=$(aws ssm describe-document --name "$PKG" --document-version "$NEW" --query Document.Status --output text)
  case $ST in Active) break;; Failed) die "document version $NEW failed";; esac; sleep 5
done
echo "published $PKG document version $NEW ($VERSION, $ST); default stays $(aws ssm describe-document --name "$PKG" --query Document.DefaultVersion --output text)"
