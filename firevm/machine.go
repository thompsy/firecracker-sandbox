package firevm

import (
	"context"
	"encoding/json"
	"fmt"
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
	GuestIP  string
	MAC      string
	Name     string
	BootedAt time.Time
}

func (s *State) String() string {
	return fmt.Sprintf("%-4d %-8d %-12s %s %s\n", s.ID, s.Pid, s.GuestIP, s.Name, s.BootedAt)
}

// BuildConfig returns the firecracker configuration for VM id. Boot is initrd-only and networking
// is a static tap on the shared bridge, addressed via the kernel ip= arg with no gateway (i.e. no
// internet).
func BuildConfig(id int) firecracker.Config {
	bootArgs := fmt.Sprintf(
		"console=ttyS0 reboot=k panic=1 pci=off ip=%s:::%s:%s:eth0:off",
		GuestIP(id), Netmask, VMName(id),
	)
	return firecracker.Config{
		SocketPath:      Socket(id),
		KernelImagePath: Kernel(),
		InitrdPath:      Initramfs(),
		KernelArgs:      bootArgs,
		Drives:          nil,
		NetworkInterfaces: firecracker.NetworkInterfaces{{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				MacAddress:  MAC(id),
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
func Launch(ctx context.Context, id int) (*State, error) {
	err := SetupTap(id)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = DelTap(id)
		_ = os.Remove(Socket(id))
	}

	err = os.MkdirAll(RunDir(), 0o755)
	if err != nil {
		return nil, err
	}

	log, err := os.Create(LogPath(id))
	if err != nil {
		cleanup()
		return nil, err
	}
	// The child process has its own file descriptor so we can close this one.
	defer log.Close()

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(FCBin()).
		WithSocketPath(Socket(id)).
		WithStdout(log).
		WithStderr(log).
		Build(ctx)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	cfg := BuildConfig(id)

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
		GuestIP:  GuestIP(id),
		MAC:      MAC(id),
		Name:     VMName(id),
		BootedAt: time.Now(),
	}
	err = SaveState(s)
	if err != nil {
		return nil, err
	}

	return s, nil
}

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

// TODO I'm not sure if i need this one really
func LoadStateFromId(id int) (*State, error) {
	b, err := os.ReadFile(StatePath(id))
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

func RemoveState(id int) error {
	return os.Remove(StatePath(id))
}

func AllStates() ([]*State, error) {
	paths, err := filepath.Glob(fmt.Sprintf("%s/fc-*.json", StateDir()))
	if err != nil {
		return nil, err
	}
	states := make([]*State, 0, len(paths))
	for _, path := range paths {
		s, err := LoadState(path)
		if err != nil {
			// TODO should we skip here or error?
			return nil, err
		}
		states = append(states, s)
	}
	return states, nil
}
