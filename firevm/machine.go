package firevm

import (
	"context"
	"fmt"
	"io"
	"os"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	models "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

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

// Launch sets up the VM's tap, then starts firecracker (our bin/firecracker) with the given stdio
// wired to the guest's serial console, and returns the running Machine. The caller must call
// Machine.Wait and then the returned cleanup func.
func Launch(ctx context.Context, id int, stdin io.Reader, stdout, stderr io.Writer) (*firecracker.Machine, func(), error) {
	if err := SetupTap(id); err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = DelTap(id)
		_ = os.Remove(Socket(id))
	}

	// NewMachine's validation rejects a pre-existing socket.
	_ = os.Remove(Socket(id))

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(FCBin()).
		WithSocketPath(Socket(id)).
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		Build(ctx)

	m, err := firecracker.NewMachine(ctx, BuildConfig(id), firecracker.WithProcessRunner(cmd))
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("new machine: %w", err)
	}
	if err := m.Start(ctx); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("start vm %d: %w", id, err)
	}
	return m, cleanup, nil
}
