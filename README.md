# firecracker-test

Spin up minimal [Firecracker](https://firecracker-microvm.github.io/) microVMs. Each VM boots a
kernel and a small busybox initramfs straight to a shell on the serial console.

All VMs share one L2 bridge (`fc-br0`, subnet `172.16.0.0/24`), so they can talk to each other and
to the host (`172.16.0.1`). There is no NAT and no default gateway in the guests, so the VMs have no
internet access. The network is host and VMs only.

## Requirements

- x86_64 Linux with KVM
- Go 1.23+ (builds the `firevm` CLI), plus `curl`, `cpio` and `iptables`
- `sudo`: the `firevm` launcher creates tap devices via netlink, so `make run`/`detach`/`stop` run it as root

## Quick start

```bash
# Download deps (firecracker, kernel, busybox), build the initramfs + firevm CLI
make setup

# Setup the network
make net-up

# Run a vm in the foreground and drop into its shell
make run ID=N

# Run a vm in the background
make detach ID=N
```

Every VM and the host are resolvable by name from inside a guest — `host`/`fc-host` plus
`fc-vm0`..`fc-vm15` — via an `/etc/hosts` baked into the initramfs. The set of names is `MAX_VMS`
(default 16); raise it and `make initramfs` to bake in more.

## Running more VMs

Each VM `<id>` gets a tap on the shared bridge, a unique MAC and its own socket.
All guests sit on `172.16.0.0/24`, addressed `172.16.0.<id+2>`:

```bash
make detach ID=0                 # background; guest at 172.16.0.2
make detach ID=1                 # background; guest at 172.16.0.3
make run    ID=2                 # foreground console; guest at 172.16.0.4
```

From VM 2's console you can reach the others by name, e.g. `ping fc-vm0`.
From the host, `ping 172.16.0.4`. Tear down with `make stop ID=<n>`.

## Layout

| Path                     | What                                                        |
|--------------------------|-------------------------------------------------------------|
| `bin/`                   | firecracker + jailer + busybox (downloaded)                 |
| `vm/`                    | guest kernel + `initramfs.cpio` (built)                     |
| `initramfs/init`         | the guest's PID 1 — **edit this to change guest behavior**  |
| `firevm/`                | Go package: VM lifecycle via the SDK (config, tap, launch)  |
| `cmd/firevm/`            | the `firevm` CLI: run / detach / stop / list                |
| `scripts/`               | deps / build-initramfs / networking (bridge) / clean        |
| `run/`                   | per-instance sockets, logs, pidfiles                        |

## Where this is headed

The guest is defined entirely by `initramfs/init` plus whatever binaries land in the initramfs. To
boot straight into your own program (e.g. a static Go + eBPF binary), drop it into the staging tree
in `scripts/build-initramfs.sh` and either call it from `init` or make it PID 1 directly.
