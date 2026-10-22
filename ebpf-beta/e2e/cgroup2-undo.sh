#!/usr/bin/env bash
# Undo run-e2e.sh's hybrid cgroup2 mount on cgroup-v1 hosts.
set -u
mountpoint -q /sys/fs/cgroup/unified && sudo umount /sys/fs/cgroup/unified
if [[ -d /sys/fs/cgroup/unified ]]; then
  sudo mount -o remount,rw /sys/fs/cgroup && sudo rmdir /sys/fs/cgroup/unified; sudo mount -o remount,ro /sys/fs/cgroup
fi
echo "cgroup2 e2e mount removed: $(ls -d /sys/fs/cgroup/unified 2>/dev/null || echo ok)"
