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

## Runtime switches (spans, EMF)

RED metrics are always exported. Spans (to the X-Ray OTLP endpoint; needs Transaction Search)
and EMF (log group `/aws/cwagent/ebpf`, namespace `CWAgent/eBPF`) are off until switched on in
an SSM String parameter, without restarting the agent:

```
aws ssm put-parameter --type String --overwrite --name AmazonCloudWatch-agent-runtime \
  --value '{"version":1,"traces_export":true,"emf_export":false}'
```

Point the agent at it with environment variables of the container (EKS: the commented `env`
entries in `eks/daemonset.yaml`; ECS: add them to `environment`):

| Variable | Default | |
|---|---|---|
| `CWAGENT_RUNTIME_PARAMETER` | unset: no polling, no SSM call, switches off | parameter name (or ARN) in the agent's region; the role needs `ssm:GetParameter` on it (`CloudWatchAgentServerPolicy` allows `AmazonCloudWatch-*`) |
| `CWAGENT_RUNTIME_POLL_INTERVAL` | `300s` | read interval (+-20% jitter, at least `10s`); a change shows within about one interval |

If the parameter cannot be read (missing, invalid JSON, no permission, throttled) the agent keeps
the last values and logs a `W!` line, retrying with backoff. The role also needs X-Ray write
access for spans (`AWSXrayWriteOnlyAccess`) and `logs:PutLogEvents` for EMF
(`CloudWatchAgentServerPolicy`).

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
