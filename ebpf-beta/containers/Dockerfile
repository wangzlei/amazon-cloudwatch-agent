# Privileged eBPF container image for the CloudWatch Agent eBPF beta, built by the user
# from the published release tarballs (nothing here compiles code or downloads files).
#
# Build context (a directory you create):
#   amazon-cloudwatch-agent-amd64.tar.gz   release tarball, linux/amd64
#   amazon-cloudwatch-agent-arm64.tar.gz   release tarball, linux/arm64 (multi-arch only)
#   Dockerfile, privileged.yaml, agent.toml  from ebpf-beta/containers/
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -t <account>.dkr.ecr.<region>.amazonaws.com/cloudwatch-agent-ebpf-beta:<version> --push .
#
# The image runs only the obi receiver pipeline of privileged.yaml and exports OTLP to
# the regular CloudWatch Agent (official image) on the same node: see README.md.
ARG BASE_IMAGE=public.ecr.aws/amazonlinux/amazonlinux:2023

# Arch-independent unpacking: runs on the build platform, so a multi-arch build needs
# no emulation.
FROM --platform=$BUILDPLATFORM $BASE_IMAGE AS unpack
ARG TARGETARCH
RUN dnf install -y tar gzip ca-certificates && dnf clean all
COPY amazon-cloudwatch-agent-${TARGETARCH}.tar.gz /tmp/agent.tar.gz
RUN mkdir -p /tmp/x /out && tar -xzf /tmp/agent.tar.gz -C /tmp/x \
 && tar -xzf /tmp/x/amazon-cloudwatch-agent-ebpf-beta/payload.tar.gz -C /tmp/x \
 && mkdir -p /out/opt/aws/amazon-cloudwatch-agent/bin /out/opt/aws/amazon-cloudwatch-agent/etc/ebpf \
             /out/opt/aws/amazon-cloudwatch-agent/var /out/opt/aws/amazon-cloudwatch-agent/logs /out/tmp \
 && cp /tmp/x/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent \
       /tmp/x/opt/aws/amazon-cloudwatch-agent/bin/CWAGENT_VERSION /out/opt/aws/amazon-cloudwatch-agent/bin/ \
 && cp /tmp/x/opt/aws/amazon-cloudwatch-agent/LICENSE /tmp/x/opt/aws/amazon-cloudwatch-agent/NOTICE \
       /tmp/x/opt/aws/amazon-cloudwatch-agent/THIRD-PARTY-LICENSES /out/opt/aws/amazon-cloudwatch-agent/ \
 && chmod 1777 /out/tmp
COPY privileged.yaml agent.toml /out/opt/aws/amazon-cloudwatch-agent/etc/ebpf/

FROM scratch
COPY --from=unpack /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=unpack /out/ /
ENV RUN_IN_CONTAINER="True"
ENTRYPOINT ["/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent", \
  "-config", "/opt/aws/amazon-cloudwatch-agent/etc/ebpf/agent.toml", \
  "-otelconfig", "/opt/aws/amazon-cloudwatch-agent/etc/ebpf/privileged.yaml"]
