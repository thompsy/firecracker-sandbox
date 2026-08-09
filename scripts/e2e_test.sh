#!/usr/bin/env bash
#
# e2e_test.sh — end-to-end smoke test for the firevmd stack.
#
# Boots two VMs on the shared bridge, has VM1 send TCP to a listener on VM0, and
# asserts the flow shows up in firevmd's /stats. Exits 0 on PASS, non-zero on FAIL.
#
# Needs root (daemon, taps, bridge, eBPF). Run via `make e2e` or `sudo scripts/e2e_test.sh`.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FIREVM=bin/firevm
FIREVMD=bin/firevmd
SOCK=run/firevmd.sock
LOG=run/e2e.log
PORT=4444
VM0_IP=172.16.0.2          # guest id 0 -> 172.16.0.(0+2)
STARTUP_TRIES=100          # x0.1s to wait for the daemon socket
FLOW_TRIES=30              # x1s to wait for the flow to appear

log()  { echo "[e2e] $*"; }
fail() {
  echo "[e2e] FAIL: $*" >&2
  [ -f "$LOG" ] && { echo "--- firevmd log ---" >&2; cat "$LOG" >&2; }
  exit 1
}

[ "$(id -u)" -eq 0 ] || { echo "run as root: sudo $0  (or: make e2e)" >&2; exit 1; }

# Preflight: the runtime assets the VMs need (produced by `make setup`).
{ [ -x "$FIREVMD" ] && [ -x "$FIREVM" ]; }        || fail "binaries missing — run 'make build'"
{ [ -f bin/firecracker ] && [ -f vm/initramfs.cpio ]; } || fail "VM assets missing — run 'make setup'"

DPID=""
cleanup() {
  log "tearing down"
  "$FIREVM" stop 1 >/dev/null 2>&1 || true
  "$FIREVM" stop 0 >/dev/null 2>&1 || true
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  scripts/host-net.sh down >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p run

log "bringing up the bridge"
scripts/host-net.sh up >/dev/null

log "starting firevmd (log -> $LOG)"
rm -f "$SOCK"                # drop a stale socket from a previous run
"$FIREVMD" >"$LOG" 2>&1 &
DPID=$!

# Wait until the daemon actually answers. It binds the socket only after
# Reconcile finishes, and a stale socket file can linger from a previous run,
# so probe a real request rather than just checking the file exists.
for _ in $(seq 1 "$STARTUP_TRIES"); do
  "$FIREVM" list >/dev/null 2>&1 && break
  kill -0 "$DPID" 2>/dev/null || fail "firevmd exited during startup"
  sleep 0.1
done
"$FIREVM" list >/dev/null 2>&1 || fail "firevmd not responding on $SOCK"

# Clear any VMs recovered from a previous run's leftover state, so the
# launches below can't hit a 409 "vm already exists".
"$FIREVM" stop 0 >/dev/null 2>&1 || true
"$FIREVM" stop 1 >/dev/null 2>&1 || true

log "launching VM0 — TCP listener on :$PORT"
"$FIREVM" run --cmd "while true; do nc -l -p $PORT; done" 0

log "launching VM1 — connects to VM0 (retrying until its listener is up)"
"$FIREVM" run --cmd "for i in 1 2 3 4 5 6 7 8 9 10; do echo hello-from-vm1 | nc -w1 $VM0_IP $PORT && break; sleep 1; done" 1

log "waiting for the VM-to-VM flow to appear in /stats"
for _ in $(seq 1 "$FLOW_TRIES"); do
  if "$FIREVM" stats 2>/dev/null | grep -q ":$PORT"; then
    echo
    "$FIREVM" stats
    echo
    log "PASS: VM-to-VM flow on :$PORT observed in /stats"
    exit 0
  fi
  sleep 1
done

fail "no flow on :$PORT after ${FLOW_TRIES}s"
