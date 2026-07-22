# Shared configuration for the firecracker-test scripts.
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

FC_VERSION="v1.16.1"
ARCH="$(uname -m)"
KERNEL_VERSION="6.1.128"
BUSYBOX_VERSION="1.35.0"

BIN_DIR="$PROJECT_ROOT/bin"
VM_DIR="$PROJECT_ROOT/vm"
RUN_DIR="$PROJECT_ROOT/run"
INITRAMFS_DIR="$PROJECT_ROOT/initramfs"

mkdir -p "$BIN_DIR" "$VM_DIR" "$RUN_DIR"

FC_BIN="$BIN_DIR/firecracker"
BUSYBOX="$BIN_DIR/busybox"
KERNEL="$VM_DIR/vmlinux-$KERNEL_VERSION"
INITRAMFS="$VM_DIR/initramfs.cpio"

# Network: all VMs share one L2 bridge + subnet so they can talk to each other
# and to the host. No NAT/default-gateway => no internet access.
#   bridge (host)  : 172.16.0.1   on fc-br0
#   guest eth0     : 172.16.0.<id+2>
BRIDGE="fc-br0"
BRIDGE_IP="172.16.0.1"
GUEST_NET="172.16.0.0/24"
MAX_VMS="${MAX_VMS:-16}"   # number of fc-vmN entries baked into the guest /etc/hosts

# Used by build-initramfs.sh to bake /etc/hosts. The VM launch/stop path now
# lives in the firevm Go CLI (which re-implements this scheme in firevm/config.go).
vm_guest_ip() { echo "172.16.0.$(( $1 + 2 ))"; }
vm_name()     { echo "fc-vm$1"; }
