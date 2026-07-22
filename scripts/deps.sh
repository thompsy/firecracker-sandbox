#!/usr/bin/env bash
# Download the firecracker/jailer binaries, the guest kernel, and a static
# busybox. Idempotent: re-running is a no-op once files are present.
set -euo pipefail
source "$(dirname "$0")/env.sh"

if [ ! -x "$FC_BIN" ]; then
  echo "Downloading firecracker $FC_VERSION for $ARCH..."
  tmp="$(mktemp -d)"
  curl -sSL "https://github.com/firecracker-microvm/firecracker/releases/download/$FC_VERSION/firecracker-$FC_VERSION-$ARCH.tgz" \
    | tar -xz -C "$tmp"
  rel="$tmp/release-$FC_VERSION-$ARCH"
  cp "$rel/firecracker-$FC_VERSION-$ARCH" "$FC_BIN"
  cp "$rel/jailer-$FC_VERSION-$ARCH" "$BIN_DIR/jailer"
  chmod +x "$FC_BIN" "$BIN_DIR/jailer"
  rm -rf "$tmp"
else
  echo "firecracker already present: $FC_BIN"
fi

if [ ! -f "$KERNEL" ]; then
  echo "Downloading guest kernel $KERNEL_VERSION..."
  curl -sSL -o "$KERNEL" \
    "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.12/$ARCH/vmlinux-$KERNEL_VERSION"
else
  echo "kernel already present: $KERNEL"
fi

if [ ! -x "$BUSYBOX" ]; then
  echo "Downloading static busybox $BUSYBOX_VERSION..."
  curl -sSL -o "$BUSYBOX" \
    "https://busybox.net/downloads/binaries/$BUSYBOX_VERSION-$ARCH-linux-musl/busybox"
  chmod +x "$BUSYBOX"
else
  echo "busybox already present: $BUSYBOX"
fi

echo "deps ready: $("$FC_BIN" --version | head -1)"
