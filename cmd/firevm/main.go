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
	"strconv"
	"syscall"

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
				Usage:     "boot a VM in the background",
				ArgsUsage: "<id>",
				Action:    withID(run),
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

	state, err := firevm.Launch(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("Started VM: %#+v\n", state)
	return nil
}

// stop signals a backgrounded VM's supervisor to shut down (which cleans up its
// tap and pidfile). If no supervisor pidfile exists, it cleans the tap directly.
func stop(_ context.Context, id int) error {
	s, err := firevm.LoadState(firevm.StatePath(id))
	if err != nil {
		return err
	}

	if err := syscall.Kill(s.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("signal supervisor pid %d: %w", s.Pid, err)
	}
	err = firevm.RemoveState(id)
	if err != nil {
		return err
	}

	fmt.Printf("VM %d (pid %d) stopping.\n", id, s.Pid)
	return nil
}

// list prints the VMs that have a supervisor pidfile in the run dir.
func list() error {
	states, err := firevm.AllStates()
	if err != nil {
		return err
	}

	if len(states) == 0 {
		fmt.Println("no VMs running")
		return nil
	}

	for _, s := range states {
		fmt.Printf("%#+v\n", s)
		status := "dead"
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", s.Pid)); err == nil {
			status = "running"
		}

		fmt.Printf("%s - %s", s, status)
	}
	return nil
}
