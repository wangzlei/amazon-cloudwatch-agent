# CloudWatch Agent eBPF beta on EKS and ECS

One container per node: the agent with the OBI receiver compiled in, from the image that
`ebpf-beta/ci/run.sh image` builds (`ebpf-beta/image/Dockerfile`). The container holds the
eBPF capabilities and the AWS credentials. Neither file uses the
`amazon-cloudwatch-observability` add-on.

| | File | Capabilities | eBPF on |
|---|---|---|---|
| EKS | `eks/daemonset.yaml`: one DaemonSet, host PID + host network | root, drop ALL + the 12 eBPF capabilities | marker `etc/ebpf/enabled` from a ConfigMap |
| ECS on EC2 | `ecs/task-definition.json`: one DAEMON task, host PID + host network | `privileged` (ECS cannot add CAP_BPF / PERFMON / CHECKPOINT_RESTORE) | `CW_OTEL_CONFIG_CONTENT` = the content of `etc/ebpf/obi.yaml` (ECS cannot mount the marker file) |

```
# EKS
kubectl apply -f eks/daemonset.yaml        # after replacing <IMAGE> and <REGION>
eksctl create podidentityassociation --cluster <c> --namespace amazon-cloudwatch \
  --service-account-name cloudwatch-agent-ebpf --permission-policy-arns arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy
# ECS (task role with CloudWatchAgentServerPolicy)
aws ecs register-task-definition --cli-input-json file://ecs/task-definition.json
aws ecs create-service --cluster <c> --service-name cloudwatch-agent-ebpf \
  --task-definition cloudwatch-agent-ebpf --scheduling-strategy DAEMON
```

## Required host mounts

| Mount | Why |
|---|---|
| `/sys/fs/cgroup` (host) | OBI attaches its sockops program (the TCP option that sets `peer.service.name`) to the root of `/sys/fs/cgroup` as the container sees it. In the container's own cgroup namespace that root is only the container's cgroup: RED still works, but no client call gets `peer.service.name`. |
| `/sys/fs/bpf` | pinned maps (`/sys/fs/bpf/otel`) |
| `/sys/kernel/tracing`, `/sys/kernel/debug` | tracepoints / kprobes |

Allow ~30 s for shutdown (`terminationGracePeriodSeconds` / `stopTimeout`): detaching the
probes takes longer than the 10 s default.

## Notes

- On Kubernetes, `tcp_service_name` is also exchanged with the cluster's Service addresses:
  a client connected to a Service sees the ClusterIP as its peer. The addresses come from the
  `ServiceCIDR` objects (Kubernetes 1.33+), or else from each Service's ClusterIPs. This needs
  the `servicecidrs` / `services` read access in the ClusterRole.
- Run one OBI per node: two would attach sockops twice and share the pins in `/sys/fs/bpf/otel`.
- Verified 2026-10-29 on EKS 1.34 (AL2023) and ECS on EC2 (AL2023): RED and `peer.service.name`
  on every client call.
- Fargate is not supported.
