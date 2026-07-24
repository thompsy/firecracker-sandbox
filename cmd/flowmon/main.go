// Command flowmon attaches the flowcount eBPF program to Firecracker VM tap
// devices and prints each VM's ingress/egress packet & byte counters on an
// interval. Requires root (loading eBPF + attaching TCX).
//
//	sudo flowmon <id> [<id>...]
//
// Each <id> refers to a running VM (its tap is fc-tap<id>).
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/thompsy/firecracker-sandbox/firevm"
	"github.com/thompsy/firecracker-sandbox/flow"
)

func main() {
	cmd := &cli.Command{
		Name:      "flowmon",
		Usage:     "print per-VM traffic counters via eBPF on Firecracker tap devices (needs root)",
		ArgsUsage: "<id> [<id>...]",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:    "interval",
				Aliases: []string{"i"},
				Value:   time.Second,
				Usage:   "refresh interval",
			},
		},
		Action: run,
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "flowmon:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c *cli.Command) error {
	ids := c.Args().Slice()
	if len(ids) == 0 {
		return fmt.Errorf("need at least one VM <id>")
	}

	m, err := flow.Load()
	if err != nil {
		return err
	}
	defer m.Close()

	// ifindex -> VM label, for the requested VMs.
	label := map[uint32]string{}
	for _, arg := range ids {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return fmt.Errorf("invalid id %q", arg)
		}
		tap := firevm.TapName(id)
		iface, err := net.InterfaceByName(tap)
		if err != nil {
			return fmt.Errorf("%s not found (is VM %d running? try: make detach ID=%d): %w", tap, id, id, err)
		}
		if err := m.Attach(tap); err != nil {
			return err
		}
		label[uint32(iface.Index)] = firevm.VMName(id)
	}
	fmt.Printf("Attached to %d tap(s). Ctrl-C to stop.\n", len(label))

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(c.Duration("interval"))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\ndetaching.")
			return nil
		case <-ticker.C:
			printStats(m, label)
		}
	}
}

func printStats(m *flow.Monitor, label map[uint32]string) {
	samples, err := m.Stats()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		return
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].Ifindex != samples[j].Ifindex {
			return samples[i].Ifindex < samples[j].Ifindex
		}
		return !samples[i].Egress // ingress before egress
	})

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VM\tDIR\tPACKETS\tBYTES")
	for _, s := range samples {
		name := label[s.Ifindex]
		if name == "" {
			name = fmt.Sprintf("if%d", s.Ifindex)
		}
		dir := "in"
		if s.Egress {
			dir = "out"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", name, dir, s.Packets, s.Bytes)
	}
	tw.Flush()
	fmt.Println()
}
