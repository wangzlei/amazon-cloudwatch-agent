# CloudWatch Agent eBPF beta on EKS and ECS

The package has two binaries, and each runs as its own process:

| Process | Binary | Privileges | Talks to |
|---|---|---|---|
| agent | `bin/amazon-cloudwatch-agent` (entrypoint `start-amazon-cloudwatch-agent`) | none, non-root (uid 10001 in the image) | listens on the Unix socket `var/ebpf/otlp.sock`; sends to CloudWatch with its AWS credentials |
| eBPF | `bin/amazon-cloudwatch-agent-ebpf` (OBI, config `ebpf/obi.yaml`) | the 12 eBPF capabilities | pushes OTLP/gRPC to the socket; sends nothing to AWS, listens on nothing |

The agent loads its eBPF receiving pipeline (`etc/ebpf/receiver.yaml`) when
`CWAGENT_EBPF_ENABLED=true` (on a host: `etc/ebpf/enabled`). The pipeline completes Lambda
function ARNs, converts the metrics to delta and sends them to CloudWatch OTLP. The socket
directory is shared only by the two containers: an emptyDir in the pod, or a task volume.

## 1. Build the image

One image holds the whole package. Both containers use it with different commands.

```
mkdir ctx && cd ctx
cp .../amazon-cloudwatch-agent-<version>.linux-amd64.tar.gz amazon-cloudwatch-agent-amd64.tar.gz   # verify the sha256
cp .../amazon-cloudwatch-agent-<version>.linux-arm64.tar.gz amazon-cloudwatch-agent-arm64.tar.gz
cp <repo>/ebpf-beta/containers/Dockerfile .
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <account>.dkr.ecr.<region>.amazonaws.com/cloudwatch-agent-ebpf-beta:<version> --push .
```

The Dockerfile only unpacks the tarball into a `scratch` image and makes `etc/`, `logs/`
and `var/` owned by the agent's uid 10001. It compiles and downloads nothing.

## 2. EKS: one DaemonSet, two containers per pod (`eks/daemonset.yaml`)

```
kubectl apply -f daemonset.yaml        # after replacing <IMAGE> and <REGION>
eksctl create podidentityassociation --cluster <c> --namespace amazon-cloudwatch \
  --service-account-name cloudwatch-agent-ebpf --permission-policy-arns arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy
```

The pod uses host PID and host network.
- `cloudwatch-agent` runs as uid/gid 10001, drops ALL capabilities and has no privilege
  escalation.
- `cloudwatch-agent-ebpf` runs as uid 0 with gid 10001. It drops ALL capabilities and adds
  only the 12. Kubernetes has no ambient capabilities, so added capabilities only work for
  root. Without `CAP_DAC_OVERRIDE`, root needs the agent's group to connect to the `0770`
  socket.

The AWS credentials are pod-level, so the eBPF container could read them too. Its config
sends nothing to AWS. The `amazon-cloudwatch-observability` add-on is not needed. To run
this next to the add-on, the add-on's operator would have to support a second container
and `hostPID`.

## 3. ECS on EC2: one DAEMON task, two containers (`ecs/task-definition.json`)

```
aws ecs register-task-definition --cli-input-json file://task-definition.json
aws ecs create-service --cluster <c> --service-name cloudwatch-agent-ebpf \
  --task-definition cloudwatch-agent-ebpf --scheduling-strategy DAEMON
```

Both containers use host network and host PID, and share a task-scoped Docker volume for the
socket.
- `cloudwatch-agent` runs as user 10001.
- `cloudwatch-agent-ebpf` is `privileged`: ECS cannot add `CAP_BPF`, `PERFMON` or
  `CHECKPOINT_RESTORE`.

The task role's credentials are given to every container of the task.

## Required host mounts (eBPF container)

| Mount | Why |
|---|---|
| `/sys/fs/cgroup` (host) | OBI attaches its sockops program (the TCP option that sets `peer.service.name`) to the root of `/sys/fs/cgroup` as the container sees it. In the container's own cgroup namespace that root is only the container's cgroup: RED still works, but no client call gets `peer.service.name`. |
| `/sys/fs/bpf` | pinned maps (`/sys/fs/bpf/otel`) |
| `/sys/kernel/tracing`, `/sys/kernel/debug` | tracepoints / kprobes |

Allow ~30 s for shutdown (`terminationGracePeriodSeconds` / `stopTimeout`): detaching the
probes takes longer than the 10 s default.

## Notes

- On Kubernetes, `tcp_service_name` is also exchanged with the cluster's Service addresses,
  because a client connected to a Service sees the ClusterIP as its peer. The addresses come
  from the `ServiceCIDR` objects (Kubernetes 1.33+), or else from each Service's ClusterIPs.
  This needs the `servicecidrs` / `services` read access in the ClusterRole.
- The agent removes a socket left by a killed agent before it starts. Otherwise the receiver
  would fail with `address already in use`.
- Fargate is not supported.
