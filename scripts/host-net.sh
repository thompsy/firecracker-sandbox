#!/usr/bin/env bash
# Create/remove the shared VM bridge. All VM taps attach here, so VMs can talk
# to each other and to the host (bridge IP), but there is NO NAT and the guests
# get no default route -> no internet access.
set -euo pipefail
source "$(dirname "$0")/env.sh"

case "${1:-}" in
  up)
    if ! ip link show "$BRIDGE" >/dev/null 2>&1; then
      echo "Creating bridge $BRIDGE ($BRIDGE_IP) [sudo]..."
      sudo ip link add "$BRIDGE" type bridge
      sudo ip addr add "$BRIDGE_IP/24" dev "$BRIDGE"
      sudo ip link set "$BRIDGE" up
    else
      echo "bridge $BRIDGE already present"
    fi
    # Permit forwarding between ports of our bridge even if a global FORWARD
    # policy (e.g. Docker's) defaults to DROP. This is intra-bridge only; there
    # is still no NAT, so it grants no internet access.
    sudo iptables -C FORWARD -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT 2>/dev/null \
      || sudo iptables -I FORWARD 1 -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT
    echo "VMs share $GUEST_NET on $BRIDGE; host reachable at $BRIDGE_IP. No internet."
    ;;
  down)
    sudo iptables -D FORWARD -i "$BRIDGE" -o "$BRIDGE" -j ACCEPT 2>/dev/null || true
    if ip link show "$BRIDGE" >/dev/null 2>&1; then
      echo "Removing bridge $BRIDGE [sudo]..."
      sudo ip link set "$BRIDGE" down
      sudo ip link del "$BRIDGE"
    fi
    echo "done."
    ;;
  *)
    echo "usage: host-net.sh up|down" >&2
    exit 1
    ;;
esac
