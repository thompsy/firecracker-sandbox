# firevmd — implementation handbook

A recoverable Firecracker control-plane daemon. This is a build-it-yourself guide:
design, concrete shapes, the tricky bits called out, and a phased order so each step is
independently testable.

## Goal & decisions

Introduce `firevmd`, a long-lived daemon that owns VM lifecycle + eBPF flow monitoring.
firecracker runs as **independent detached processes** that survive the daemon
crashing/restarting; on startup the daemon **reconnects to survivors** (dead VMs are
reconciled/cleaned, not restarted). The CLI (`firevm`) becomes a thin client.

Why: removes the `__supervise` re-exec + pidfiles and the cross-process eBPF pinning
gymnastics, and makes flow↔VM-metadata correlation (PLAN.md step 4) trivial. It's the
substrate for the Part II AI-sandbox control plane.

Verified facts this relies on:

- SDK `Machine` is **spawn-only**, but `firecracker.NewUnixSocketTransport(sock)` +
  `client.New(t, strfmt.Default)` give the **full REST API against a running socket** —
  the durable control handle for reconnection. Confirm method names via
  `go doc .../client/operations` (e.g. `DescribeInstance`, `CreateSyncAction`).
- eBPF survives restart via `link.Pin`/`link.LoadPinnedLink`, `Map.Pin`/`ebpf.LoadPinnedMap`,
  `Program.Pin`/`ebpf.LoadPinnedProgram`.

## Layout

```
cmd/firevmd/main.go   # daemon entrypoint: load flow, reconcile, serve
cmd/firevm/main.go    # (refactor) thin client over the unix socket
daemon/
  server.go           # http.Serve on unix socket; routes + Reconcile + reap
  registry.go         # in-mem map[int]*VM; VM type (control-client) + Alive
api/                  # shared request/response structs (imported by daemon + client)
firevm/
  machine.go          # Launch (detached) + BuildConfig
  config.go           # add DaemonSock(), StateDir(), StatePath(id), pin paths
  network.go          # unchanged (SetupTap/DelTap)
flow/
  flow.go             # + pinning (map/prog/links), adopt-on-recovery, Stats unchanged
  flowcount.c, *_bpf* # unchanged
```

## Persisted state (`run/vms/fc-<id>.json`)

The source of truth for recovery — one file per VM, written at launch, deleted at stop.

```go
type State struct {
    ID       int
    Pid      int       // firecracker pid
    Socket   string    // API socket path
    Tap      string
    GuestIP  string
    MAC, Name string
    BootedAt time.Time
}
```

Add paths to `firevm/config.go`: `DaemonSock()=run/firevmd.sock`, `StateDir()=run/vms`,
`StatePath(id)`, and pin paths under `PinDir="/sys/fs/bpf/firevm"`:
`MapPin=PinDir/flows`, `ProgPinIngress/Egress`, `LinkPin(id,dir)`.

## 1. Detached launch (refactor `firevm/machine.go`)

`Launch` sets up the tap, then starts firecracker **detached** so it outlives the daemon:

```go
func Launch(ctx context.Context, id int, cmd string) (*State, error) {
    SetupTap(id)                                   // existing netlink
    log, _ := os.Create(logPath(id))               // run/fc-<id>.log
    c := firecracker.VMCommandBuilder{}.
        WithBin(fcBinary()).WithSocketPath(Socket(id)).
        WithStdout(log).WithStderr(log).            // NOT the caller's stdio
        Build(ctx)
    c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // own session -> survives daemon death
    cfg := BuildConfig(id, cmd)                    // cmd -> base64 firevm_cmd boot arg (see below)
    cfg.ForwardSignals = []os.Signal{}             // daemon SIGTERM must NOT reach the VM
    m, _ := firecracker.NewMachine(ctx, cfg, firecracker.WithProcessRunner(c))
    m.Start(ctx)                                   // spawn(detached) + configure + boot
    pid, _ := m.PID()
    // do NOT m.Wait(); discard m — the detached firecracker keeps running.
    return &State{ID:id, Pid:pid, Socket:Socket(id), ...}, nil
}
```

Key points, each a real gotcha:

- **`Setsid`** detaches from the daemon's session so the child isn't signalled when the
  daemon exits (a child isn't killed by parent exit on its own, but this removes SIGHUP).
- **stdio → file**, so firecracker doesn't write into a daemon pipe that breaks (SIGPIPE)
  when the daemon dies.
- **`ForwardSignals = []os.Signal{}`** — otherwise the SDK forwards the daemon's signals
  to firecracker, killing VMs on a daemon restart.
- **Don't `Wait`; discard `m`.** The SDK sets no process-killing finalizer, so the
  detached process outlives the `Machine`.

**Running a command at boot (`--cmd`).** `BuildConfig(id, cmd)` base64-encodes `cmd` into a
`firevm_cmd=` kernel-cmdline token (base64 so the kernel doesn't split it on spaces); the
`initramfs/init` PID 1 decodes and runs it. It threads through as
`firevm run --cmd "…" <id>` → `api.LaunchRequest.Cmd` → `Launch`. Handy for testing (boot one
VM running `nc -l …`, another to connect).

## 2. Control via the API socket (`daemon/registry.go` + `daemon/server.go`)

```go
// registry.go — the control client is dialed when the VM is registered.
func NewVM(s *firevm.State) *VM {
    return &VM{State: s,
        Client: client.New(firecracker.NewUnixSocketTransport(s.Socket, nil, false), strfmt.Default)}
}
// Alive: the socket is the authority — a reused pid can't answer it. Socket probe only
// (no /proc pre-check: a live pid with a dead API is dead to us anyway).
func (vm *VM) Alive() bool {
    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()
    _, err := vm.Client.Operations.DescribeInstance(operations.NewDescribeInstanceParamsWithContext(ctx))
    return err == nil
}
// server.go — reap cleans up a stopped/dead VM's leftovers; shared by handleStop + Reconcile.
func reap(id int) { firevm.DelTap(id); os.Remove(firevm.Socket(id)); firevm.RemoveState(id) }
// handleStop SIGTERMs the pid, then reap()s. (No poll-then-SIGKILL fallback yet — a later hardening.)
```

A `VM` in the registry holds `{State, *client.Firecracker}`. The client is only needed for
liveness/actions (describe, snapshot later); launch/stop use the pid + netlink + flow.

## 3. Recovery (`daemon/server.go`)

On daemon start, before serving:

```
Reconcile():                       # daemon/server.go, before Run()
  for each state in firevm.AllStates():
    vm := NewVM(state)
    if vm.Alive():                 # socket probe (DescribeInstance)
      registry.Add(vm)             # re-adopt the survivor
      # Phase 2+: flow.Attach(id) here — the old daemon's links died with it
    else:
      reap(id)                     # DelTap + remove socket + remove state
  # Phase 3: flow.Init() (adopt pinned map/prog) moves to the top; flow.Adopt on survivors
```

## 4. eBPF in the daemon (`flow/flow.go`)

Shared design: **one** program + **one** `ifindex`-keyed map, attached per-tap. Pin so it
survives a daemon restart. Requires bpffs at `/sys/fs/bpf` (check it's mounted; `mkdir`
the `firevm` subdir).

- `flow.Init()` — if `MapPin` exists: `LoadPinnedMap` + `LoadPinnedProgram` (adopt).
  Else: load the collection, `Flows.Pin(MapPin)`, `CountIngress.Pin`/`CountEgress.Pin`.
  (Program handles are needed to attach *new* VMs after a restart; the map handle is
  needed for stats.)
- `Attach(id)` — `AttachTCX` ingress+egress on `fc-tap<id>`, then `link.Pin(LinkPin(id,dir))`.
- `Adopt(id)` — `LoadPinnedLink` the two links (keeps them alive; lets `Detach` unpin).
- `Detach(id)` — `Unpin()` + `Close()` both links.
- `Stats()` — iterate the in-memory `Flows` map (unchanged logic). The daemon joins with
  the registry for VM name / boot time → the correlation.
- **Idempotency:** remove a stale pin before `Pin` (mirrors the idempotent tap setup).

## 5. Daemon HTTP API (`daemon/server.go`)

`net.Listen("unix", DaemonSock())` (remove a stale socket first) + `http.Serve`. JSON in/out.

| Method & path        | Action                                             |
|----------------------|----------------------------------------------------|
| `POST /vms {id,cmd}` | `Launch` + save state + register (+ `flow.Attach` in Phase 2)     |
| `DELETE /vms/{id}`   | SIGTERM the pid + `reap`                                          |
| `GET /vms`           | registry → `[]State` (trimmed `VMView` DTO with `alive` is Phase 4) |
| `GET /stats`         | `flow.Stats()` joined with registry (Phase 2)                    |

Shared request/response structs live in `api/` (imported by both sides). **Graceful
shutdown closes the listener but leaves VMs running** — that's the whole point.

## 6. CLI client (`cmd/firevm/main.go`)

Keep the `urfave/cli` verbs; each becomes an HTTP call over the unix socket:

```go
http.Client{Transport: &http.Transport{
    DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
        return net.Dial("unix", firevm.DaemonSock())
    }}}
// then GET/POST http://unix/vms etc.
```

- `run`/`detach` collapse → keep `run` = "launch" (all VMs are detached now).
- Add `stats`; add `logs <id>` = tail `run/fc-<id>.log` directly (same host, no API).
- Delete `__supervise`, the re-exec, and pidfiles.

## 7. Console tradeoff

Detached VMs send the serial console to the log file, so the **interactive foreground
console is gone**. v1: `firevm logs <id>` (tail). A reattachable console (firecracker
serial → PTY/unix socket) is out of scope; note for later.

## 8. Cleanup & permissions

- `make clean` (scripts/clean.sh): `DELETE /vms/*` (or SIGTERM firecrackers), remove
  `run/firevmd.sock`, `rm -rf /sys/fs/bpf/firevm`, clear `run/vms/`.
- `firevmd` runs as **root** (netlink, eBPF, kvm). Socket is root-owned; `firevm` reaches
  it via `sudo` (or a socket group). `make run/stop/...` keep using sudo, now targeting the
  daemon. Add a `make daemon` target (`sudo bin/firevmd`).

## Implementation order (each phase independently testable)

- **Phase 0 — detached + daemon skeleton.** Refactor `Launch` (detached); state files; daemon
  with `POST/DELETE/GET /vms` (no recovery, no eBPF yet); CLI → client. *Test:* launch/stop/
  list via the daemon; VM boots; `ping` works.
- **Phase 1 — recovery.** `Reconcile()` re-adopts survivors (VMs only; on restart just
  re-`flow.Attach` fresh later). *Test:* `kill -9` the daemon → VM still runs → restart →
  `list` shows it re-adopted.
- **Phase 2 — flow in the daemon.** Shared map, `GET /stats` with correlation. eBPF **not
  pinned yet** — accept that stats reset on daemon restart (re-attach fresh, clean orphan
  TCX links). *Test:* TCP traffic shows in `stats`.
- **Phase 3 — pin eBPF.** Pin map/prog/links; `flow.Init/Adopt/Detach`. *Test:* the full
  recovery test below — stats survive a daemon restart.
- **Phase 4 — polish.** `logs`, `make clean`/`daemon`, stale-socket handling, errors when
  bpffs missing.

## Verification (end-to-end)

1. `make build`; `sudo bin/firevmd` (foreground).
2. `firevm run 0`; `firevm list`/`stats`; TCP between guests → `stats` counts.
3. **Recovery:** `sudo kill -9 <firevmd>`; confirm VM still runs (`ping fc-vm0`). Restart
   `firevmd`; `list` shows it re-adopted; `stats` resumes (pins).
4. **Reconcile:** `sudo kill <a firecracker pid>`; restart daemon → that VM marked gone,
   tap/pin/state cleaned.
5. `go vet ./... && go test ./...`.

## Gotchas checklist

- bpffs mounted at `/sys/fs/bpf`? (else `mount -t bpf bpf /sys/fs/bpf`).
- `ForwardSignals=[]`, `Setsid`, stdio→file — the three things that keep VMs alive past
  the daemon.
- Don't `m.Wait()`; discard the `Machine`.
- `flow.Init` must **adopt** pins on restart, not re-`Load` (a second `Load` = a second,
  empty map).
- Remove stale `run/firevmd.sock` and stale pins before (re)creating.
- Reconnect uses the low-level `client`, not `Machine` (no reattach constructor).

## Out of scope (later)

Restart/resume of dead VMs (snapshots), reattachable interactive console, systemd unit +
socket auth, and unloading the shared program when the last VM stops.
