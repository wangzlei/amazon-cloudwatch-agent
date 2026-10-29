#!/bin/sh
# Installs the CloudWatch Agent eBPF beta from this tarball with the same files,
# user and systemd units as the RPM (packaging/linux/amazon-cloudwatch-agent.spec):
# "amazon-cloudwatch-agent-ctl -a enable-ebpf" starts the eBPF process
# (amazon-cloudwatch-agent-ebpf.service, user cwagent, the only process with the eBPF
# capabilities). Do not grant them with setcap or run it as root instead.
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
for f in etc/common-config.toml ebpf/obi.yaml etc/ebpf/receiver.yaml; do
    [ -e "$A/$f" ] && set -- "$@" "--exclude=opt/aws/amazon-cloudwatch-agent/$f"
done
tar -C / -xzf "$here/payload.tar.gz" --no-same-owner --no-overwrite-dir "$@"

# %files %attr: the eBPF socket directory, setgid group cwagent.
chown root:cwagent "$A/var/ebpf" && chmod 2770 "$A/var/ebpf"

# %posttrans: drop the in-agent beta's drop-in; eBPF opted in, enable the eBPF unit.
rm -f /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf
rmdir /etc/systemd/system/amazon-cloudwatch-agent.service.d 2>/dev/null || :
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    if [ -f "$A/etc/ebpf/enabled" ]; then systemctl enable amazon-cloudwatch-agent-ebpf.service; fi
fi
if $upgrade; then "$CTL" -a cond-restart || :; fi

echo "Installed $(cat "$A/bin/CWAGENT_VERSION"). eBPF is off by default; to enable it:"
echo "  sudo $CTL -a enable-ebpf"
