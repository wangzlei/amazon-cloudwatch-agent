# CloudWatch Agent eBPF beta on EKS and ECS (two containers)

The eBPF beta runs on EKS and ECS on EC2 as two containers per node:

| | Image | Runs | Credentials |
|---|---|---|---|
| **privileged** | built by you from the release tarball (`Dockerfile` here) | the `obi` receiver only (`privileged.yaml`): obi → transform → cumulativetodelta → batch → OTLP/HTTP to `127.0.0.1:4328` | none |
| **regular** | the official CloudWatch Agent (EKS add-on `amazon-cloudwatch-observability`, or `public.ecr.aws/cloudwatch-agent/cloudwatch-agent` on ECS), unchanged | its usual config plus `opentelemetry.collect.otlp` on loopback; sends the RED metrics to CloudWatch (`monitoring` `/v1/metrics`) | its own (Pod Identity / IRSA, ECS task role) |

Both use the host network, so the OTLP receiver stays on the node's loopback. The privileged
container reads instance metadata (VPC CIDRs, EC2 resource attributes) but holds no AWS
credentials and exports nothing to AWS itself. Requires CloudWatch Agent ≥ 1.300074.0 for the
regular agent (the `opentelemetry` config section).

## 1. Build the privileged image

```
mkdir ctx && cd ctx
# release tarballs (verify the published sha256)
cp .../amazon-cloudwatch-agent-<version>.linux-amd64.tar.gz amazon-cloudwatch-agent-amd64.tar.gz
cp .../amazon-cloudwatch-agent-<version>.linux-arm64.tar.gz amazon-cloudwatch-agent-arm64.tar.gz
cp <repo>/ebpf-beta/containers/{Dockerfile,privileged.yaml,agent.toml} .
aws ecr create-repository --repository-name cloudwatch-agent-ebpf-beta
aws ecr get-login-password | docker login --username AWS --password-stdin <account>.dkr.ecr.<region>.amazonaws.com
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <account>.dkr.ecr.<region>.amazonaws.com/cloudwatch-agent-ebpf-beta:<version> --push .
```

The Dockerfile only unpacks the tarball and copies files into a `scratch` image (agent binary,
CA bundle, `privileged.yaml`, `agent.toml`); it compiles and downloads nothing. For a single
architecture, pass only that `--platform` and tarball.

## 2. EKS

1. Add `opentelemetry` to the add-on's agent config (`eks/addon-config.json`; fill in region and
   cluster name). `agent.config` **replaces** the add-on default, so keep the sections you use
   (the file keeps the defaults: Container Insights and Application Signals):
   ```
   aws eks update-addon --cluster-name <cluster> --addon-name amazon-cloudwatch-observability \
     --configuration-values file://addon-config.json
   ```
   Do not send the eBPF metrics to the add-on's 4315/4316: that is the Application Signals
   pipeline, which renames and filters metrics.
2. Deploy the privileged DaemonSet (`eks/privileged-daemonset.yaml`, set `<IMAGE>`):
   `kubectl apply -f privileged-daemonset.yaml`. It runs in `amazon-cloudwatch` with host PID and
   host network, as root with only the 12 capabilities (not `privileged`), and a service
   account that can only read Kubernetes metadata.

Nothing is installed on the node, so Bottlerocket and EKS Auto Mode nodes should work too (tested: AL2023 managed nodes only). Fargate: not supported.

## 3. ECS on EC2

Two daemon services per cluster, both `networkMode: host`:

1. Regular agent: `ecs/cwagent-task-definition.json` (official image, task role with
   `CloudWatchAgentServerPolicy`); `CW_CONFIG_CONTENT` = `ecs/cwagent-config.json` as one line
   (or from SSM Parameter Store via `secrets`).
2. Privileged: `ecs/privileged-task-definition.json` (your image, `privileged: true`,
   `pidMode: host`, **no task role**). ECS cannot add CAP_BPF / PERFMON / CHECKPOINT_RESTORE, so
   the container is privileged.

```
aws ecs register-task-definition --cli-input-json file://cwagent-task-definition.json
aws ecs register-task-definition --cli-input-json file://privileged-task-definition.json
aws ecs create-service --cluster <c> --service-name cwagent --task-definition cwagent-daemon --scheduling-strategy DAEMON
aws ecs create-service --cluster <c> --service-name cwagent-ebpf --task-definition cwagent-ebpf-privileged --scheduling-strategy DAEMON
```

The regular agent must be a daemon on the same host, not a sidecar: the privileged container sees
every task on the host and has one destination.

## Required host mounts

| Mount | Why |
|---|---|
| `/sys/fs/cgroup` (host) | OBI attaches its sockops program (TCP-option `peer.service.name`) to the root of `/sys/fs/cgroup` as the container sees it. Container runtimes give the container a private cgroup namespace, whose root is only the container's own cgroup: without the host mount, RED works but no client call gets `peer.service.name`. |
| `/sys/fs/bpf` | pinned maps (`/sys/fs/bpf/otel`) |
| `/sys/kernel/tracing`, `/sys/kernel/debug` | tracepoints / kprobes |

Allow ~30 s for shutdown (`terminationGracePeriodSeconds` / `stopTimeout`): detaching the probes
takes longer than the 10 s default.

## Known limitations

- On Kubernetes, `tcp_service_name` is also exchanged with the cluster's Service addresses
  (a client connected to a Service sees the ClusterIP, which is usually outside the VPC): the
  `ServiceCIDR` objects (Kubernetes 1.33+), else each Service's ClusterIPs. This needs the
  `servicecidrs` / `services` read access in the DaemonSet's ClusterRole. Pod IPs outside the
  VPC (overlay CNIs) are not added.
- **ECS: `aws.ecs.task.arn` is the regular agent's task**, not the application's: the agent's
  `opentelemetry` pipeline runs resource detection with `override: true`. Cluster, host and
  `service.name` are correct; per-task attribution needs ECS metadata in the privileged container.
- **EKS: `k8s.pod.ip` is `127.0.0.1`**: the agent's k8sattributes processor sets it from the
  connection (loopback). The other `k8s.*` attributes come from OBI and are correct.
- The privileged container runs as root and pins maps in `/sys/fs/bpf/otel` as root; an RPM agent
  (user `cwagent`) on the same host then fails with "load pinned map: permission denied". Do not
  run both on one host.
