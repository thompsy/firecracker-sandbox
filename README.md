# firecracker-sandbox

Lightweight [Firecracker](https://firecracker-microvm.github.io/) microVM sandboxes with per-VM eBPF
network observability with a small Go control plane for isolating (and watching) untrusted
workloads.

Each VM boots a kernel and a tiny busybox initramfs and joins a private L2 network shared with the
host and the other VMs. A tc/TCX eBPF program on each VM's tap device records its traffic as 5-tuple
flows.

VM lifecycle is owned by a small root daemon, `firevmd`. The `firevm` CLI is a thin client that
drives it over a unix socket.

This project is designed purely as a learning vehicle for me.

## Requirements

- x86_64 Linux with KVM (`/dev/kvm`)
- Go 1.23+, and `clang`/LLVM (compiles the eBPF program)
- `curl`, `cpio`, `iptables`
- `sudo`: creating taps, the bridge, and loading eBPF need root

## Quick start

```bash
# download firecracker, kernel, and busybox and build the initramfs, eBPF, and CLIs
make setup

# create the shared bridge fc-br0 (requires sudo)
make net-up

# start the control-plane daemon in the foreground (requires sudo)
make daemon

# --- in another terminal ---
# launch VM 0 in the background; console logged to run/fc-0.log (requires sudo)
make run ID=0

# or launch it running a command at boot:
sudo bin/firevm run --cmd "nc -l -p 4444" 0

# show running VMs
make list

# stop it
make stop ID=0

# tear everything down: VMs, taps, bridge (sudo)
make clean
```

VMs run detached — watch a VM's console with `tail -f run/fc-0.log`, and use `--cmd` to run a
workload at boot.

## Networking

All VMs share one L2 bridge `fc-br0` on `172.16.0.0/24`:

- host `172.16.0.1`  ·  VM `<id>` → `172.16.0.<id+2>`
- No NAT and no default route in the guests → no internet by design. VMs reach each other and the
  host only.

Inside a guest, the host and every VM resolve by name (`host`, `fc-vm0`…`fc-vm15`) via an
`/etc/hosts` baked into the initramfs (count is `MAX_VMS`, default 16).

## eBPF flow monitor

`flowmon` attaches a tc/TCX eBPF program to a VM's tap device and prints its traffic as per-flow
counters, `(src ip:port → dst ip:port, proto, packets, bytes)`, keyed by interface, so every flow is
attributed to a VM by construction.

```bash
make run ID=0
# attach and print counters (requires sudo). Ctrl-C to detach
make flowmon ID=0

# from the host, generate some TCP and watch the counters move
nc -w1 172.16.0.2 4444
```

The program is `flow/flowcount.c` (compiled by `bpf2go`, regenerate with `make bpf`); `flow/flow.go`
loads it and attaches via TCX (host kernel ≥ 6.6).

## Layout

| Path                     | What                                                                       |
|--------------------------|----------------------------------------------------------------------------|
| `firevm/`                | Go package: VM config/scheme, netlink taps, detached launch, on-disk state |
| `daemon/`                | control-plane daemon: in-memory registry + HTTP server over a unix socket  |
| `cmd/firevmd/`           | the `firevmd` daemon entrypoint                                             |
| `cmd/firevm/`            | the `firevm` CLI (client for firevmd): `run` / `stop` / `list`             |
| `api/`                   | request/response types shared by firevm and firevmd                        |
| `flow/` + `cmd/flowmon/` | eBPF flow monitor: tc/TCX program + loader + CLI                           |
| `initramfs/init`         | the guest's PID 1 (busybox) — edit to change guest behaviour               |
| `scripts/` + `Makefile`  | deps / build-initramfs / bridge / clean                                    |
| `bin/`, `vm/`, `run/`    | downloaded / built / runtime artifacts (gitignored)                        |
