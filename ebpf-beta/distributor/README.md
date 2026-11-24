# SSM Distributor package: ADOT-CloudWatchAgent-eBPF-Beta

The scripts and manifest of the Distributor package (account 292779133546,
us-west-2, shared with 868155213681). Each arch zip contains
`amazon-cloudwatch-agent.rpm` (the RPM this branch builds, `ebpf-beta/ci/run.sh rpm`)
plus `install.sh` and `uninstall.sh` from this directory.

| File | What |
|---|---|
| `install.sh`, `uninstall.sh` | the package's install / uninstall scripts (run by `AWS-ConfigureAWSPackage`) |
| `manifest.template.json` | Distributor manifest; `build-zips.sh` fills in the version and the zips' sha256 |
| `build-zips.sh` | `<x86_64.rpm> <aarch64.rpm> <versionName> <outdir>`: the two zips + `manifest.json` (no AWS) |
| `publish-scripts.sh` | publishes already-built RPMs as a new, non-default document version |

- Install without arguments: behaves like the official CloudWatch Agent package
  (installs, does not start, no eBPF).
- Install with Additional Arguments `{"SSM_EBPF":"true"}`: installs and starts the
  agent with eBPF (`amazon-cloudwatch-agent-ctl -a enable-ebpf`). Distributor passes
  each key, which must start with `SSM_`, as an environment variable.
- Optional Additional Argument `SSM_CONFIG_PARAMETER`, e.g.
  `{"SSM_EBPF":"true","SSM_CONFIG_PARAMETER":"AmazonCloudWatch-ebpf-demo-config"}`: on a FRESH
  install, after the RPM (and eBPF) step, install.sh also runs
  `amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c ssm:<name> -s` (applies that JSON to
  etc/amazon-cloudwatch-agent.d/ and restarts the agent; etc/ebpf/obi.yaml is not touched, so eBPF
  stays on). The instance role needs ssm:GetParameter on it (CloudWatchAgentServerPolicy allows
  `AmazonCloudWatch-*`). A failed fetch (missing parameter, no permission, invalid config) does
  not fail the install: an `ERROR: ... SSM parameter '<name>'` line goes to stderr (the
  "install errors" part of the Run Command output) and the agent keeps its previous (default)
  config. Not fetched when the host already has an agent config (`etc/amazon-cloudwatch-agent.toml`)
  and this is a version change / reinstall (`etc/config.reinstall` from uninstall.sh, or the
  older markers `etc/restart`, `etc/ebpf/enabled.reinstall`, or the RPM still installed): the
  config the erase kept is used. A Distributor Uninstall keeps the config too (like the official
  package), so a later Install also keeps it; run `fetch-config` on the host to change it.
- A version change in Distributor is uninstall (old package's uninstall.sh, full
  `rpm -e`) then install, so the eBPF choice is carried across via
  `etc/ebpf/enabled.reinstall` (and `etc/restart`), like the official package.

## CI

The `rpm` job of `.github/workflows/ebpf-beta.yml` runs `build-zips.sh` on its two RPMs and
uploads the result as the artifact `distributor-<version>`
(`amazon-cloudwatch-agent-ebpf-beta_{x86_64,arm64}.zip` + `manifest.json`). CI does not
publish: it has no AWS credentials. The `scripts` gate runs `bash -n` / `sh -n` and shellcheck on
these scripts (`ebpf-beta/ci/check-scripts.sh`).

## Publish

From a host with the AWS credentials of 292779133546 (ada, Admin), `rpm`, `zip`, `jq`:

```
gh run download <run-id> -R wangzlei/amazon-cloudwatch-agent -n rpm-<version> -D /tmp/rpm-<version>
ebpf-beta/distributor/publish-scripts.sh /tmp/rpm-<version> <versionName>
# or: publish-scripts.sh build/ebpf-beta/bin/linux <versionName>      (after ebpf-beta/ci/run.sh rpm)
# or: publish-scripts.sh <x86_64.rpm> <aarch64.rpm> <versionName>
```

It zips the RPMs with this directory's install.sh/uninstall.sh (`build-zips.sh`, the same layout
as the CI artifact), uploads to a new `s3://adot-obi-distributor-292779133546-usw2/<versionName>/`
and adds a NON-default document version (default and share unchanged). versionName cannot
contain `+` (use the `-` form of the version, e.g. `1.300074.0-ebpfbeta.<sha7>`). To make it the
default after testing:
`aws ssm update-document-default-version --region us-west-2 --name ADOT-CloudWatchAgent-eBPF-Beta --document-version <n>`.
