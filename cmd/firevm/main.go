// Command firevm launches and manages minimal Firecracker microVMs.
//
//	firevm run <id>      boot a VM on this terminal's serial console
//	firevm detach <id>   boot a VM in the background (supervised)
//	firevm stop <id>     stop a backgrounded VM
//	firevm list          list running VMs
//
// It creates the per-VM tap via netlink, so it must run as root. The bridge
// must already exist (make net-up).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/thompsy/firecracker-sandbox/firevm"
)

func main() {
	cmd := &cli.Command{
		Name:  "firevm",
		Usage: "launch and manage minimal Firecracker microVMs",
		Commands: []*cli.Command{
			{
				Name:      "run",
				Usage:     "boot a VM on this terminal's serial console",
				ArgsUsage: "<id>",
				Action:    withID(run),
			},
			{
				Name:      "detach",
				Usage:     "boot a VM in the background",
				ArgsUsage: "<id>",
				Action:    withID(detach),
			},
			{
				Name:      "stop",
				Usage:     "stop a backgrounded VM",
				ArgsUsage: "<id>",
				Action:    withID(stop),
			},
			{
				Name:   "list",
				Usage:  "list running VMs",
				Action: func(context.Context, *cli.Command) error { return list() },
			},
			{
				Name:      "__supervise", // internal: the detached child process
				Hidden:    true,
				ArgsUsage: "<id>",
				Action:    withID(supervise),
			},
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "firevm:", err)
		os.Exit(1)
	}
}

// withID adapts a handler taking a single VM id into a cli.ActionFunc, parsing
// and validating the one positional <id> argument.
func withID(fn func(context.Context, int) error) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		if c.Args().Len() != 1 {
			return fmt.Errorf("expected a single <id> argument")
		}
		arg := c.Args().First()
		id, err := strconv.Atoi(arg)
		if err != nil || id < 0 {
			return fmt.Errorf("invalid id %q", arg)
		}
		return fn(ctx, id)
	}
}

// run boots a VM on this terminal's console and blocks until it exits.
func run(ctx context.Context, id int) error {
	fmt.Printf("Booting VM %d (%s) on the serial console. Guest IP: %s\n",
		id, firevm.VMName(id), firevm.GuestIP(id))
	fmt.Println("Type CTRL-D inside the VM to shut down and return here.")
	fmt.Println()

	m, cleanup, err := firevm.Launch(ctx, id, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer cleanup()
	return m.Wait(ctx)
}

// detach re-execs this binary as a detached supervisor that owns the VM.
func detach(ctx context.Context, id int) error {
	logf, err := os.Create(firevm.LogPath(id))
	if err != nil {
		return err
	}
	defer logf.Close()

	self, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(self, "__supervise", strconv.Itoa(id))
	child.Stdout = logf
	child.Stderr = logf
	child.Stdin = nil
	child.Env = os.Environ()
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(firevm.PidPath(id), []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
		return err
	}

	fmt.Printf("VM %d (%s) started (pid %d), guest ip %s\n", id, firevm.VMName(id), child.Process.Pid, firevm.GuestIP(id))
	fmt.Printf("  log:  %s\n", firevm.LogPath(id))
	fmt.Printf("  stop: firevm stop %d\n", id)
	return nil
}

// supervise is the detached child: it runs the VM, cleans up on exit, and stops
// the VM on SIGTERM/SIGINT.
func supervise(ctx context.Context, id int) error {
	defer os.Remove(firevm.PidPath(id))

	m, cleanup, err := firevm.Launch(ctx, id, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer cleanup()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		// Best effort graceful shutdown, then force the VMM down.
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.Shutdown(shCtx)
		_ = m.StopVMM()
	}()
	return m.Wait(ctx)
}

// stop signals a backgrounded VM's supervisor to shut down (which cleans up its
// tap and pidfile). If no supervisor pidfile exists, it cleans the tap directly.
func stop(_ context.Context, id int) error {
	data, err := os.ReadFile(firevm.PidPath(id))
	if err != nil {
		_ = firevm.DelTap(id)
		_ = os.Remove(firevm.Socket(id))
		fmt.Printf("VM %d: no supervisor pidfile; removed tap.\n", id)
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("bad pidfile %s: %w", firevm.PidPath(id), err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("signal supervisor pid %d: %w", pid, err)
	}
	fmt.Printf("VM %d (pid %d) stopping.\n", id, pid)
	return nil
}

// list prints the VMs that have a supervisor pidfile in the run dir.
func list() error {
	files, _ := filepath.Glob(filepath.Join(firevm.RunDir(), "fc-*.pid"))
	if len(files) == 0 {
		fmt.Println("no VMs running")
		return nil
	}
	fmt.Printf("%-4s %-8s %-8s %-12s %s\n", "ID", "PID", "STATE", "GUEST IP", "NAME")
	for _, f := range files {
		var id int
		if _, err := fmt.Sscanf(filepath.Base(f), "fc-%d.pid", &id); err != nil {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		state := "dead"
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
			state = "running"
		}
		fmt.Printf("%-4d %-8d %-8s %-12s %s\n", id, pid, state, firevm.GuestIP(id), firevm.VMName(id))
	}
	return nil
}
