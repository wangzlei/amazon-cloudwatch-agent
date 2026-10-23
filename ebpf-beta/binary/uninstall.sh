#!/bin/sh
# Removes what install.sh installed, like "rpm -e" (%preun and %postun of
# packaging/linux/amazon-cloudwatch-agent.spec). Keeps the cwagent user, as the RPM does.
#   sudo ./uninstall.sh
set -eu
[ "$(id -u)" -eq 0 ] || { echo "uninstall.sh: run as root" >&2; exit 1; }
A=/opt/aws/amazon-cloudwatch-agent
if [ -x "$A/bin/amazon-cloudwatch-agent-ctl" ]; then "$A/bin/amazon-cloudwatch-agent-ctl" -a preun || :; fi
rm -f /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf
rmdir /etc/systemd/system/amazon-cloudwatch-agent.service.d 2>/dev/null || :
rm -f /etc/systemd/system/amazon-cloudwatch-agent.service /etc/init/amazon-cloudwatch-agent.conf \
      /usr/bin/amazon-cloudwatch-agent-ctl /etc/amazon/amazon-cloudwatch-agent \
      /var/log/amazon/amazon-cloudwatch-agent /var/run/amazon/amazon-cloudwatch-agent
rm -rf "$A"
if [ -d /run/systemd/system ]; then systemctl daemon-reload; fi
echo "Removed the CloudWatch Agent."
