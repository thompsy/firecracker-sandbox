package firevm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// State represents the state of a launched VM
type State struct {
	ID int

	// Pid is the system pid for this VM
	Pid int

	// Socket is the path to the API socket
	Socket string

	Tap      string
	Ifindex  int
	GuestIP  string
	MAC      string
	Name     string
	BootedAt time.Time
}

// String returns the string representation of the State.
func (s *State) String() string {
	return fmt.Sprintf("%-4d %-8d %-12s %s %s\n", s.ID, s.Pid, s.GuestIP, s.Name, s.BootedAt)
}

// BuildConfig returns the firecracker configuration for VM id. Boot is initrd-only and networking
// is a static tap on the shared bridge, addressed via the kernel ip= arg with no gateway (i.e. no
// internet).
func BuildConfig(id int, cmd string) firecracker.Config {
	bootArgs := fmt.Sprintf(
		"console=ttyS0 reboot=k panic=1 pci=off ip=%s:::%s:%s:eth0:off",
		guestIP(id), Netmask, VMName(id),
	)

	// If we've passed a command line, encode that in base64 (so the kernel doesn't split on
	// spaces) and append it to bootArgs
	if cmd != "" {
		bootArgs += " firevm_cmd=" + base64.StdEncoding.EncodeToString([]byte(cmd))
	}
	return firecracker.Config{
		SocketPath:      Socket(id),
		KernelImagePath: kernel(),
		InitrdPath:      initramfs(),
		KernelArgs:      bootArgs,
		Drives:          nil,
		NetworkInterfaces: firecracker.NetworkInterfaces{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  mac(id),
				HostDevName: TapName(id),
			},
		}},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(1),
			MemSizeMib: firecracker.Int64(256),
		},
	}
}

// Launch sets up the VM's tap, then starts firecracker in detached mode. Stdout and stderr are
// wired to a log file.
func Launch(ctx context.Context, id int, cmdLine string) (*State, error) {
	err := SetupTap(id)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = DelTap(id)
		_ = os.Remove(Socket(id))
	}

	// The tap now exists; grab its ifindex so State can carry it for flow correlation.
	iface, err := net.InterfaceByName(TapName(id))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("resolve tap %s: %w", TapName(id), err)
	}

	err = os.MkdirAll(RunDir(), 0o755)
	if err != nil {
		return nil, err
	}

	log, err := os.Create(logPath(id))
	if err != nil {
		cleanup()
		return nil, err
	}
	// The child process has its own file descriptor so we can close this one.
	defer log.Close()

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(fcBinary()).
		WithSocketPath(Socket(id)).
		WithStdout(log).
		WithStderr(log).
		Build(ctx)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	cfg := BuildConfig(id, cmdLine)

	// daemon SIGTERM must NOT reach the VM
	cfg.ForwardSignals = []os.Signal{}

	// Clear a socket left by a previous VM
	_ = os.Remove(Socket(id))

	m, err := firecracker.NewMachine(ctx, cfg, firecracker.WithProcessRunner(cmd))
	if err != nil {
		cleanup()
		return nil, err
	}

	// spawn(detached) + configure + boot
	err = m.Start(ctx)
	if err != nil {
		cleanup()
		return nil, err
	}

	pid, err := m.PID()
	if err != nil {
		cleanup()
		return nil, err
	}

	s := &State{
		ID:       id,
		Pid:      pid,
		Socket:   Socket(id),
		Tap:      TapName(id),
		Ifindex:  iface.Index,
		GuestIP:  guestIP(id),
		MAC:      mac(id),
		Name:     VMName(id),
		BootedAt: time.Now(),
	}
	// Persistence is the daemon's responsibility (it owns the on-disk registry),
	// so Launch just boots the VM and returns the facts.
	return s, nil
}

// SaveState writes out the given State to its associated file.
func SaveState(s *State) error {
	err := os.MkdirAll(StateDir(), 0o755)
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(StatePath(s.ID), b, 0o644)
}

// LoadState loads a State from the given path.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var s State
	err = json.Unmarshal(b, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// RemoveState removes file state file for the given VM.
func RemoveState(id int) error {
	return os.Remove(StatePath(id))
}

// AllStates reads all saved state files from disk.
func AllStates() ([]*State, error) {
	paths, err := filepath.Glob(fmt.Sprintf("%s/fc-*.json", StateDir()))
	if err != nil {
		return nil, err
	}
	states := make([]*State, 0, len(paths))
	for _, path := range paths {
		s, err := LoadState(path)
		if err != nil {
			slog.Warn("failed to load state file", "path", path, "err", err)
			continue
		}
		states = append(states, s)
	}
	return states, nil
}
