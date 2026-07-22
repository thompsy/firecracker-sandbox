#!/usr/bin/env bash
# Tear down all runtime artifacts: stop this project's VMs, remove every
# fc-tap* device and the bridge, and delete per-instance run files.
# Uses sudo only when there is actually network state to remove.
set -euo pipefail
source "$(dirname "$0")/env.sh"

# 1. Stop this project's VMs. The firevm supervisors and their firecracker
#    children run as root (launched via sudo), so kill needs sudo too. Signal
#    supervisors first (they shut their VM down and clean up), then force any
#    firecracker still bound to our run dir.
mapfile -t sups < <(pgrep -af "firevm __supervise" | awk '{print $1}')
mapfile -t fcs  < <(pgrep -af firecracker | awk -v d="$RUN_DIR/fc-" 'index($0,d){print $1}')
pids=("${sups[@]}" "${fcs[@]}")
if [ "${#pids[@]}" -gt 0 ]; then
  echo "Stopping VMs (pids: ${pids[*]}) [sudo]..."
  sudo kill "${pids[@]}" 2>/dev/null || true
fi

# 2. Remove all fc-tap* devices.
mapfile -t taps < <(ls /sys/class/net 2>/dev/null | grep -E '^fc-tap' || true)
if [ "${#taps[@]}" -gt 0 ]; then
  echo "Removing taps (${taps[*]}) [sudo]..."
  for t in "${taps[@]}"; do sudo ip link del "$t" 2>/dev/null || true; done
fi

# 3. Remove the bridge (and its iptables rule) if present.
if ip link show "$BRIDGE" >/dev/null 2>&1; then
  "$(dirname "$0")/host-net.sh" down
fi

# 4. Delete per-instance run files.
rm -f "$RUN_DIR"/*.sock "$RUN_DIR"/*.log "$RUN_DIR"/*.pid 2>/dev/null || true

echo "clean: done."
