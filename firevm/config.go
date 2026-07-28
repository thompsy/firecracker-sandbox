// Package firevm launches and manages minimal Firecracker microVMs via the
// firecracker-go-sdk. The per-VM addressing scheme mirrors scripts/env.sh.
//
// NOTE: the IP/MAC/name scheme below is duplicated in scripts/env.sh, which
// still feeds the /etc/hosts baking in scripts/build-initramfs.sh. Keep the two
// in sync until the scheme is unified into a single source.
package firevm

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	KernelVersion = "6.1.128"

	// Networking: all VMs share one L2 bridge/subnet (see scripts/host-net.sh).
	Bridge   = "fc-br0"
	BridgeIP = "172.16.0.1"
	Netmask  = "255.255.255.0"
)

// Root is the project root containing bin/, vm/ and run/. It is taken from
// $FIREVM_ROOT, else derived from the executable location (<root>/bin/firevm),
// else the current directory.
func Root() string {
	if r := os.Getenv("FIREVM_ROOT"); r != "" {
		return r
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(filepath.Dir(exe)) // <root>/bin/firevm -> <root>
	}
	wd, _ := os.Getwd()
	return wd
}

func binDir() string { return filepath.Join(Root(), "bin") }
func vmDir() string  { return filepath.Join(Root(), "vm") }

// RunDir holds per-instance sockets, logs and pidfiles.
func RunDir() string { return filepath.Join(Root(), "run") }

// Asset paths
func fcBinary() string  { return filepath.Join(binDir(), "firecracker") }
func kernel() string    { return filepath.Join(vmDir(), "vmlinux-"+KernelVersion) }
func initramfs() string { return filepath.Join(vmDir(), "initramfs.cpio") }

// Per-VM addressing scheme (mirrors scripts/env.sh).
func guestIP(id int) string { return fmt.Sprintf("172.16.0.%d", id+2) }
func TapName(id int) string { return fmt.Sprintf("fc-tap%d", id) }
func mac(id int) string     { return fmt.Sprintf("06:00:AC:10:%02x:02", id) }
func VMName(id int) string  { return fmt.Sprintf("fc-vm%d", id) }

// Per-VM run files.
func Socket(id int) string  { return filepath.Join(RunDir(), fmt.Sprintf("fc-%d.sock", id)) }
func logPath(id int) string { return filepath.Join(RunDir(), fmt.Sprintf("fc-%d.log", id)) }

func DaemonSock() string      { return filepath.Join(RunDir(), "firevmd.sock") }
func StateDir() string        { return filepath.Join(RunDir(), "vms") }
func StatePath(id int) string { return filepath.Join(StateDir(), fmt.Sprintf("fc-%d.json", id)) }
