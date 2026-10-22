# CloudWatch Agent eBPF beta image: the official CloudWatch Agent image layout
# (amazon-cloudwatch-container-insights/cloudwatch-agent-dockerfile/localbin/Dockerfile,
# entrypoint start-amazon-cloudwatch-agent) plus the eBPF default config in
# etc/ebpf/obi.yaml. Like the RPM, eBPF is off until etc/ebpf/enabled exists (mount an
# empty file there); the container then needs the eBPF capabilities (or privileged).
#
# Build context: a BUILD_SPACE after `make build-for-docker-amd64 build-for-docker-arm64
# BUILD_SPACE=<dir>` with the eBPF config copied to <dir>/obi.yaml (ebpf-beta/ci/run.sh image;
# not the repo root, whose .dockerignore excludes build/).
ARG CERT_IMAGE=ubuntu:latest

# Arch-independent (certs, directories, copies): runs on the build platform, so a
# multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM $CERT_IMAGE AS cert
ARG TARGETARCH
RUN mkdir -p /opt/aws/amazon-cloudwatch-agent/etc/ebpf /opt/aws/amazon-cloudwatch-agent/var
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY bin/linux_${TARGETARCH}/ /opt/aws/amazon-cloudwatch-agent/bin
COPY obi.yaml /opt/aws/amazon-cloudwatch-agent/etc/ebpf/obi.yaml

FROM scratch
COPY --from=cert /tmp /tmp
COPY --from=cert /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=cert /opt/aws/amazon-cloudwatch-agent /opt/aws/amazon-cloudwatch-agent
ARG VERSION=unknown
ARG OBI_SOURCE=unknown
LABEL org.opencontainers.image.title="cloudwatch-agent-ebpf-beta" \
      org.opencontainers.image.version="${VERSION}" \
      io.ebpf-beta.obi-source="${OBI_SOURCE}"
ENV RUN_IN_CONTAINER="True"
ENTRYPOINT ["/opt/aws/amazon-cloudwatch-agent/bin/start-amazon-cloudwatch-agent"]
