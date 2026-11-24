#!/bin/sh
# SSM Distributor install: CloudWatch Agent + ADOT OBI eBPF receiver (beta).
# By default this behaves like the official CloudWatch Agent package: it only
# installs (or upgrades) the RPM, and an agent that was running before an
# upgrade is restarted (prep-restart in uninstall.sh, cond-restart in the RPM).
# eBPF is opt-in: pass the Distributor additional argument {"SSM_EBPF":"true"}
# (AWS-ConfigureAWSPackage exposes it to this script as the env var SSM_EBPF)
# to install AND start the agent with eBPF, or run
#   amazon-cloudwatch-agent-ctl -a enable-ebpf
# later on the host. "-a disable-ebpf" turns it off.
# Optional {"SSM_CONFIG_PARAMETER":"<name>"}: on a fresh install, also apply the
# agent's JSON config from that SSM parameter and (re)start the agent
#   amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c ssm:<name> -s
# (the instance role needs ssm:GetParameter on it; CloudWatchAgentServerPolicy
# allows names starting with AmazonCloudWatch-). fetch-config only manages
# etc/amazon-cloudwatch-agent.d/; the eBPF config etc/ebpf/obi.yaml is kept.
# A failed fetch does not fail the install: the agent keeps its default config.
# Distributor upgrades are uninstall + install, i.e. a full rpm erase (which
# drops the eBPF state), so uninstall.sh leaves etc/ebpf/enabled.reinstall and
# this script restores the marker from it BEFORE rpm -U: the RPM's %posttrans
# then installs the drop-in and restarts the agent with eBPF. The erase keeps
# the agent config (etc/amazon-cloudwatch-agent.d/, .toml, .yaml, not owned by
# the RPM), and uninstall.sh leaves etc/config.reinstall: on an upgrade the
# config the host already has is kept and SSM_CONFIG_PARAMETER is NOT fetched.
set -e
A=/opt/aws/amazon-cloudwatch-agent
CTL=$A/bin/amazon-cloudwatch-agent-ctl
KEEP=$A/etc/ebpf/enabled.reinstall
CFG_KEEP=$A/etc/config.reinstall
EBPF="${SSM_EBPF:-false}"
PARAM="${SSM_CONFIG_PARAMETER:-}"
echo "SSM_EBPF=${EBPF}"
[ -z "$PARAM" ] || echo "SSM_CONFIG_PARAMETER=${PARAM}"

# Not a fresh install: a Distributor version change (uninstall.sh of this or an
# older version left a marker) or the RPM is already installed, and the host
# already has an agent config (the erase keeps it).
UPGRADE=false
if { [ -f "$CFG_KEEP" ] || [ -f "$KEEP" ] || [ -f $A/etc/restart ] || rpm -q amazon-cloudwatch-agent >/dev/null 2>&1; } &&
  [ -f $A/etc/amazon-cloudwatch-agent.toml ]; then
  UPGRADE=true
fi

# Migration from the always-on versions (<= 1.300074.0-obi.e3a8675): their
# uninstall.sh stops and erases without leaving any state. If the agent's last
# start was unprivileged (only the eBPF drop-in does that) and eBPF was not
# turned off with the old etc/ebpf/disabled, keep eBPF on and running.
LOG=$A/logs/amazon-cloudwatch-agent.log
if ! rpm -q amazon-cloudwatch-agent >/dev/null 2>&1 && [ ! -f "$KEEP" ] && [ ! -f $A/etc/ebpf/disabled ] && [ -f "$LOG" ] &&
  grep -E "I! Detected runAsUser|not root: skipping run_as_user" "$LOG" | tail -1 | grep -q "not root: skipping run_as_user"; then
  echo "previous always-on eBPF install detected: keeping eBPF on"
  mkdir -p $A/etc/ebpf
  touch "$KEEP" $A/etc/restart
  [ ! -f $A/etc/amazon-cloudwatch-agent.toml ] || UPGRADE=true
fi
rm -f $A/etc/ebpf/disabled
if [ -f "$KEEP" ]; then
  touch $A/etc/ebpf/enabled
  rm -f "$KEEP"
fi
# Migration: versions before 1.300074.0-obi.526f82c put the eBPF config in .d;
# with the RPM's etc/ebpf/obi.yaml it would start a second OBI receiver.
rm -f $A/etc/amazon-cloudwatch-agent.d/obi.yaml

rpm -U --replacepkgs ./amazon-cloudwatch-agent.rpm
rm -f "$CFG_KEEP"

if [ "${EBPF}" = "true" ]; then
  # Restarts the agent, or starts it (with the default config if none).
  $CTL -a enable-ebpf
  $CTL -a start
fi

if [ -n "$PARAM" ]; then
  if [ "$UPGRADE" = "true" ]; then
    echo "SSM_CONFIG_PARAMETER: upgrade / reinstall, keeping the agent config already on this host (not fetching ssm:${PARAM}); to apply it run: $CTL -a fetch-config -m ec2 -c ssm:${PARAM} -s"
  elif ! printf '%s' "$PARAM" | grep -Eq '^[A-Za-z0-9_./-]{1,2048}$'; then
    echo "ERROR: SSM_CONFIG_PARAMETER '${PARAM}' is not a valid SSM parameter name; the agent config was not changed" >&2
  elif $CTL -a fetch-config -m ec2 -c "ssm:${PARAM}" -s; then
    echo "applied the agent config from SSM parameter ${PARAM}"
  else
    echo "ERROR: could not apply the agent config from SSM parameter '${PARAM}' (missing parameter, no ssm:GetParameter permission, or invalid config: see the output above); the install succeeded and the agent keeps its previous config" >&2
    if [ "${EBPF}" = "true" ]; then
      $CTL -a start || true
    fi
  fi
fi
$CTL -a status
