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
	"encoding/binary"
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
	fmt.Fprintln(tw, "VM\tProto\tSrc\tDst\tDir\tPackets\tBytes")
	for _, s := range samples {
		name := label[s.Ifindex]
		if name == "" {
			name = fmt.Sprintf("if%d", s.Ifindex)
		}
		dir := "in"
		if s.Egress {
			dir = "out"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s:%d\t%s\t%d\t%d\n", name, proto(s.Proto), ipv4(s.SrcAddr), port(s.SrcPort), ipv4(s.DstAddr), port(s.DstPort), dir, s.Packets, s.Bytes)
	}
	tw.Flush()
	fmt.Println()
}

// __be16 port field -> host-order port number
func port(v uint16) uint16 {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], v) // recover the on-wire (network) bytes
	return binary.BigEndian.Uint16(b[:])   // read them big-endian => host value
}

// __be32 address field -> net.IP
func ipv4(v uint32) net.IP {
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], v) // network-order bytes...
	return net.IP(b[:])                    // ...which is exactly what net.IP wants
}

func proto(p uint8) string {
	if p == 6 {
		return "TCP"
	}
	if p == 17 {
		return "UDP"
	}
	return "UNK"
}
