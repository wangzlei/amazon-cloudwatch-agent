# SSM Distributor package: ADOT-CloudWatchAgent-eBPF-Beta

The scripts and manifest of the Distributor package (account 292779133546,
us-west-2, shared with 868155213681). Each arch zip contains
`amazon-cloudwatch-agent.rpm` (the RPM this branch builds, `ebpf-beta/ci/run.sh rpm`)
plus `install.sh` and `uninstall.sh` from this directory.

| File | What |
|---|---|
| `install.sh`, `uninstall.sh` | the package's install / uninstall scripts (run by `AWS-ConfigureAWSPackage`; arguments `SSM_EBPF`, `SSM_RUNTIME_PARAMETER`, `SSM_RUNTIME_POLL_INTERVAL`) |
| `manifest.template.json` | Distributor manifest; `build-zips.sh` fills in the version and the zips' sha256 |
| `build-zips.sh` | `<x86_64.rpm> <aarch64.rpm> <versionName> <outdir>`: the two zips + `manifest.json` (no AWS) |
| `publish-scripts.sh` | publishes already-built RPMs as a new, non-default document version |

- Install without arguments: behaves like the official CloudWatch Agent package
  (installs, does not start, no eBPF).
- Install with Additional Arguments `{"SSM_EBPF":"true"}`: installs and starts the
  agent with eBPF (`amazon-cloudwatch-agent-ctl -a enable-ebpf`). Distributor passes
  each key, which must start with `SSM_`, as an environment variable.
- Optional Additional Arguments `SSM_RUNTIME_PARAMETER` (and `SSM_RUNTIME_POLL_INTERVAL`, default
  `300s`, at least `10s`), e.g.
  `{"SSM_EBPF":"true","SSM_RUNTIME_PARAMETER":"AmazonCloudWatch-agent-runtime","SSM_RUNTIME_POLL_INTERVAL":"30s"}`:
  the SSM String parameter with the runtime switches of the eBPF config, read by the agent every
  poll interval without a restart:
  `{"version":1,"traces_export":false,"emf_export":false}` (spans to X-Ray / Transaction Search,
  EMF to `/aws/cwagent/ebpf` + namespace `CWAgent/eBPF`; RED metrics are always on). install.sh
  writes them to `etc/ebpf/runtime.env` (`CWAGENT_RUNTIME_PARAMETER=`,
  `CWAGENT_RUNTIME_POLL_INTERVAL=`), the `EnvironmentFile` of the eBPF systemd drop-in, before the
  RPM step. The file is not owned by the RPM: a version change or reinstall keeps it unless
  `SSM_RUNTIME_PARAMETER` is given again (then it is overwritten); remove it by hand (and restart
  the agent) to stop polling. Without the argument and without the file nothing is polled and both
  switches stay off. An invalid name is reported (`ERROR:` on stderr) and not written; the install
  goes on. The instance role needs `ssm:GetParameter` on the parameter
  (`CloudWatchAgentServerPolicy` allows `AmazonCloudWatch-*`), X-Ray write access for spans
  (`AWSXrayWriteOnlyAccess`) and the account needs Transaction Search
  (`aws xray get-trace-segment-destination` = `CloudWatchLogs`).
- A version change in Distributor is uninstall (old package's uninstall.sh, full
  `rpm -e`) then install, so the eBPF choice is carried across via
  `etc/ebpf/enabled.reinstall` (and `etc/restart`), like the official package.

Switching at run time (no agent restart; takes effect within about one poll interval, the agent
logs `runtimecontrol: switch changed` and `gate: data passes` / `is dropped`):

```
aws ssm put-parameter --region us-west-2 --type String --overwrite --name AmazonCloudWatch-agent-runtime \
  --value '{"version":1,"traces_export":true,"emf_export":false}'
```

A value that cannot be read (missing parameter, invalid JSON, no permission, throttling) keeps the
last values (`W!` line, retried with backoff up to 4 poll intervals).

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
