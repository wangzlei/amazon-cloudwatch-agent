#!/bin/sh
# Installs the CloudWatch Agent eBPF beta from this tarball with the same files,
# user and systemd units as the RPM (packaging/linux/amazon-cloudwatch-agent.spec):
# the agent runs as the unprivileged cwagent user, and "amazon-cloudwatch-agent-ctl
# -a enable-ebpf" grants the eBPF capabilities through the systemd drop-in. Do not
# grant them with setcap or run the agent as root instead.
#   sudo ./install.sh        install or upgrade
set -eu
[ "$(id -u)" -eq 0 ] || { echo "install.sh: run as root" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
A=/opt/aws/amazon-cloudwatch-agent
CTL=$A/bin/amazon-cloudwatch-agent-ctl

# %pre: stop a running agent, create the cwagent group and user.
upgrade=false
if [ -x "$CTL" ]; then
    upgrade=true
    "$CTL" -a prep-restart || :
    "$CTL" -a stop || :
fi
getent group cwagent >/dev/null 2>&1 || groupadd -r cwagent
if ! id cwagent >/dev/null 2>&1; then
    shell=/bin/false
    for s in /sbin/nologin /usr/sbin/nologin; do [ -x "$s" ] && { shell=$s; break; }; done
    useradd -r -M cwagent -d /home/cwagent -g cwagent -c "Cloudwatch Agent" -s "$shell"
fi

# Files, owned by root. Like %config(noreplace), keep configuration files the
# user already has.
set --
for f in etc/common-config.toml etc/ebpf/obi.yaml; do
    [ -e "$A/$f" ] && set -- "$@" "--exclude=opt/aws/amazon-cloudwatch-agent/$f"
done
tar -C / -xzf "$here/payload.tar.gz" --no-same-owner --no-overwrite-dir "$@"

# %posttrans: eBPF opted in, reinstall the drop-in from the root-owned template.
if [ -f "$A/etc/ebpf/enabled" ]; then
    mkdir -p /etc/systemd/system/amazon-cloudwatch-agent.service.d
    cp -f "$A/share/ebpf/systemd-dropin.conf" /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf
fi
if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
if $upgrade; then "$CTL" -a cond-restart || :; fi

echo "Installed $(cat "$A/bin/CWAGENT_VERSION"). eBPF is off by default; to enable it:"
echo "  sudo $CTL -a enable-ebpf"
