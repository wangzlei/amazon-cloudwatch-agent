#!/bin/sh
# Distributor upgrades run this (old package) and then install.sh (new one),
# like the official package: remember whether the agent ran (etc/restart) and
# whether eBPF was on (etc/ebpf/enabled.reinstall) across the full rpm erase.
A=/opt/aws/amazon-cloudwatch-agent
if [ -x $A/bin/amazon-cloudwatch-agent-ctl ]; then
  $A/bin/amazon-cloudwatch-agent-ctl -a prep-restart || true
  if [ -f $A/etc/ebpf/enabled ]; then
    touch $A/etc/ebpf/enabled.reinstall
  fi
  $A/bin/amazon-cloudwatch-agent-ctl -a stop || true
fi
set -e
rpm -e amazon-cloudwatch-agent
