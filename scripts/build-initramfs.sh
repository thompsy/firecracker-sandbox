#!/usr/bin/env bash
# Assemble vm/initramfs.cpio from the static busybox + initramfs/init.
# Pure userspace: no sudo, no mkfs, no loop mounts.
set -euo pipefail
source "$(dirname "$0")/env.sh"

[ -x "$BUSYBOX" ] || { echo "Missing busybox; run: make deps"; exit 1; }

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE"/{bin,proc,sys,dev,etc}
cp "$BUSYBOX" "$STAGE/bin/busybox"
chmod +x "$STAGE/bin/busybox"
cp "$INITRAMFS_DIR/init" "$STAGE/init"
chmod +x "$STAGE/init"

# /etc/hosts: resolve the host and all VMs by name (deterministic IP scheme).
{
  echo "127.0.0.1    localhost"
  echo "$BRIDGE_IP    host fc-host"
  for id in $(seq 0 $((MAX_VMS - 1))); do
    printf "%-12s %s\n" "$(vm_guest_ip "$id")" "$(vm_name "$id")"
  done
} > "$STAGE/etc/hosts"

# Pack the cpio (newc format). -R 0:0 records root ownership without needing
# to actually be root on the host.
( cd "$STAGE" && find . | cpio --quiet -o -H newc -R 0:0 ) > "$INITRAMFS"

echo "Built $INITRAMFS ($(du -h "$INITRAMFS" | cut -f1))"
