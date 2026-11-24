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
# Distributor upgrades are uninstall + install, i.e. a full rpm erase (which
# drops the eBPF state), so uninstall.sh leaves etc/ebpf/enabled.reinstall and
# this script restores the marker from it BEFORE rpm -U: the RPM's %posttrans
# then installs the drop-in and restarts the agent with eBPF.
# Optional {"SSM_RUNTIME_PARAMETER":"<name>"} (and "SSM_RUNTIME_POLL_INTERVAL",
# e.g. "30s"; default 300s): the SSM String parameter with the runtime switches
# of the eBPF config (spans: traces_export, EMF: emf_export; both off unless
# set, e.g. {"version":1,"traces_export":true,"emf_export":false}). Written to
# etc/ebpf/runtime.env, the EnvironmentFile of the eBPF systemd drop-in, before
# the RPM step (which starts / restarts the agent). runtime.env is not owned by
# the RPM: a version change (uninstall + install) keeps it, unless
# SSM_RUNTIME_PARAMETER is given again (then it is overwritten). Without the
# argument and without runtime.env the switches are not polled (off).
set -e
A=/opt/aws/amazon-cloudwatch-agent
CTL=$A/bin/amazon-cloudwatch-agent-ctl
KEEP=$A/etc/ebpf/enabled.reinstall
EBPF="${SSM_EBPF:-false}"
RUNTIME_ENV=$A/etc/ebpf/runtime.env
RT_PARAM="${SSM_RUNTIME_PARAMETER:-}"
RT_INTERVAL="${SSM_RUNTIME_POLL_INTERVAL:-}"
echo "SSM_EBPF=${EBPF}"
[ -z "$RT_PARAM" ] || echo "SSM_RUNTIME_PARAMETER=${RT_PARAM}"
[ -z "$RT_INTERVAL" ] || echo "SSM_RUNTIME_POLL_INTERVAL=${RT_INTERVAL}"

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
fi
rm -f $A/etc/ebpf/disabled
if [ -f "$KEEP" ]; then
  touch $A/etc/ebpf/enabled
  rm -f "$KEEP"
fi
# Migration: versions before 1.300074.0-obi.526f82c put the eBPF config in .d;
# with the RPM's etc/ebpf/obi.yaml it would start a second OBI receiver.
rm -f $A/etc/amazon-cloudwatch-agent.d/obi.yaml
# Left by the uninstall.sh of versions with SSM_CONFIG_PARAMETER; unused here.
rm -f $A/etc/config.reinstall

# Runtime switches parameter (see above). An invalid value is reported and not
# written: the install goes on, and an existing runtime.env stays as it is.
if [ -n "$RT_PARAM" ]; then
  if ! printf '%s' "$RT_PARAM" | grep -Eq '^([A-Za-z0-9_./-]{1,2048}|arn:aws[a-z-]*:ssm:[a-z0-9-]+:[0-9]{12}:parameter/[A-Za-z0-9_./-]{1,2048})$'; then
    echo "ERROR: SSM_RUNTIME_PARAMETER '${RT_PARAM}' is not a valid SSM parameter name; ${RUNTIME_ENV} not changed" >&2
  else
    if [ -n "$RT_INTERVAL" ] && ! printf '%s' "$RT_INTERVAL" | grep -Eq '^([1-9][0-9]+s|[1-9][0-9]*[mh])$'; then
      echo "ERROR: SSM_RUNTIME_POLL_INTERVAL '${RT_INTERVAL}' is not a duration of at least 10s such as 30s, 5m or 1h; using the default (300s)" >&2
      RT_INTERVAL=""
    fi
    mkdir -p $A/etc/ebpf
    TMP="${RUNTIME_ENV}.tmp.$$"
    {
      echo "# Written by the SSM Distributor package ADOT-CloudWatchAgent-eBPF-Beta (SSM_RUNTIME_PARAMETER)."
      echo "CWAGENT_RUNTIME_PARAMETER=${RT_PARAM}"
      [ -z "$RT_INTERVAL" ] || echo "CWAGENT_RUNTIME_POLL_INTERVAL=${RT_INTERVAL}"
    } > "$TMP"
    chmod 0644 "$TMP"
    mv -f "$TMP" "$RUNTIME_ENV"
    echo "runtime switches: wrote ${RUNTIME_ENV} (parameter ${RT_PARAM}, poll interval ${RT_INTERVAL:-300s})"
  fi
elif [ -n "$RT_INTERVAL" ]; then
  echo "ERROR: SSM_RUNTIME_POLL_INTERVAL needs SSM_RUNTIME_PARAMETER; ignored" >&2
elif [ -f "$RUNTIME_ENV" ]; then
  echo "runtime switches: keeping ${RUNTIME_ENV} ($(grep -E '^CWAGENT_RUNTIME_' "$RUNTIME_ENV" | tr '\n' ' '))"
else
  echo "runtime switches: no SSM_RUNTIME_PARAMETER, not polled (spans and EMF off)"
fi

rpm -U --replacepkgs ./amazon-cloudwatch-agent.rpm

if [ "${EBPF}" = "true" ]; then
  # Restarts the agent, or starts it (with the default config if none).
  $CTL -a enable-ebpf
  $CTL -a start
fi
$CTL -a status
