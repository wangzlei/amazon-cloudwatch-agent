# CloudWatch Agent eBPF beta image, built by the user from the release tarball (nothing here
# compiles code or downloads files). One image with the whole package: the agent
# (entrypoint start-amazon-cloudwatch-agent, as the official image) and the eBPF process
# (bin/amazon-cloudwatch-agent-ebpf, OBI). Run them as two containers of one pod / task
# that share the socket directory var/ebpf: see README.md.
#
# Build context (a directory you create):
#   amazon-cloudwatch-agent-amd64.tar.gz   release tarball, linux/amd64
#   amazon-cloudwatch-agent-arm64.tar.gz   release tarball, linux/arm64 (multi-arch only)
#   Dockerfile                             this file
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -t <account>.dkr.ecr.<region>.amazonaws.com/cloudwatch-agent-ebpf-beta:<version> --push .
ARG BASE_IMAGE=public.ecr.aws/amazonlinux/amazonlinux:2023
# The agent container runs as this non-root user: it owns the dirs the agent writes
# (etc/: the translated config, logs/, var/: pid file and the eBPF socket).
ARG AGENT_UID=10001

# Arch-independent unpacking: runs on the build platform, so a multi-arch build needs
# no emulation.
FROM --platform=$BUILDPLATFORM $BASE_IMAGE AS unpack
ARG TARGETARCH
ARG AGENT_UID
RUN dnf install -y tar gzip ca-certificates && dnf clean all
COPY amazon-cloudwatch-agent-${TARGETARCH}.tar.gz /tmp/agent.tar.gz
RUN mkdir -p /tmp/x /out/tmp && tar -xzf /tmp/agent.tar.gz -C /tmp/x \
 && tar -xzf /tmp/x/amazon-cloudwatch-agent-ebpf-beta/payload.tar.gz -C /tmp/x \
 && cp -a /tmp/x/opt /out/ && A=/out/opt/aws/amazon-cloudwatch-agent \
 && rm -rf $A/var/ebpf && mkdir -p $A/var/ebpf $A/logs \
 && chown -R ${AGENT_UID}:${AGENT_UID} $A/etc $A/logs $A/var && chmod 0770 $A/var/ebpf \
 && chmod 1777 /out/tmp

FROM scratch
COPY --from=unpack /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=unpack /out/ /
ENV RUN_IN_CONTAINER="True"
ENTRYPOINT ["/opt/aws/amazon-cloudwatch-agent/bin/start-amazon-cloudwatch-agent"]
