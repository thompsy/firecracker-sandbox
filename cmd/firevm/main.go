// Command firevm is a thin client for the firevmd control-plane daemon. It
// sends run/stop/list requests over the daemon's unix socket; firevmd (which
// runs as root) does the actual VM management.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/thompsy/firecracker-sandbox/api"
	"github.com/thompsy/firecracker-sandbox/firevm"
)

// baseURL's host is ignored — the transport's DialContext always dials the
// daemon's unix socket. The URL just has to parse.
const baseURL = "http://unix"

func getClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", firevm.DaemonSock())
			},
		},
	}
}

// request issues an HTTP request to firevmd over its unix socket. The caller
// owns resp.Body.
func request(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := getClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach firevmd (is it running? try: sudo bin/firevmd): %w", err)
	}
	return resp, nil
}

func main() {
	cmd := &cli.Command{
		Name:  "firevm",
		Usage: "launch and manage minimal Firecracker microVMs (client for firevmd)",
		Commands: []*cli.Command{
			{
				Name:      "run",
				Usage:     "launch a VM in the background",
				ArgsUsage: "<id>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "cmd", Usage: "command to run in the VM at boot"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					id, err := parseID(c)
					if err != nil {
						return err
					}
					return run(ctx, id, c.String("cmd"))
				},
			},
			{
				Name:      "stop",
				Usage:     "stop a running VM",
				ArgsUsage: "<id>",
				Action:    withID(stop),
			},
			{
				Name:   "list",
				Usage:  "list running VMs",
				Action: func(context.Context, *cli.Command) error { return list() },
			},
			{
				Name:   "stats",
				Usage:  "print per-VM traffic counters",
				Action: func(context.Context, *cli.Command) error { return stats() },
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "firevm:", err)
		os.Exit(1)
	}
}

// parseID parses and validates the single positional <id> argument.
func parseID(c *cli.Command) (int, error) {
	if c.Args().Len() != 1 {
		return 0, fmt.Errorf("expected a single <id> argument")
	}
	arg := c.Args().First()
	id, err := strconv.Atoi(arg)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid id %q", arg)
	}
	return id, nil
}

// withID adapts a handler taking a single VM id into a cli.ActionFunc, parsing
// and validating the one positional <id> argument.
func withID(fn func(context.Context, int) error) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		id, err := parseID(c)
		if err != nil {
			return err
		}
		return fn(ctx, id)
	}
}

// run asks firevmd to launch VM id in the background.
func run(_ context.Context, id int, cmd string) error {
	body, err := json.Marshal(api.LaunchRequest{
		ID:  id,
		Cmd: cmd,
	})
	if err != nil {
		return err
	}
	resp, err := request(http.MethodPost, "/vms", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("launch failed (%s): %s", resp.Status, bytes.TrimSpace(b))
	}

	var s firevm.State
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	fmt.Printf("started VM %d (%s) — pid %d, ip %s\n", s.ID, s.Name, s.Pid, s.GuestIP)
	return nil
}

// stop asks firevmd to stop VM id.
func stop(_ context.Context, id int) error {
	resp, err := request(http.MethodDelete, "/vms/"+strconv.Itoa(id), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		fmt.Printf("stopped VM %d\n", id)
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("no VM with id %d", id)
	default:
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stop failed (%s): %s", resp.Status, bytes.TrimSpace(b))
	}
}

// list prints the VMs firevmd is managing.
func list() error {
	resp, err := request(http.MethodGet, "/vms", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("list failed (%s): %s", resp.Status, bytes.TrimSpace(b))
	}

	var states []firevm.State
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Println("no VMs running")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tIP\tPID")
	for _, s := range states {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\n", s.ID, s.Name, s.GuestIP, s.Pid)
	}
	tw.Flush()
	return nil
}

// stats prints the per-VM flow counters firevmd is tracking.
func stats() error {
	resp, err := request(http.MethodGet, "/stats", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stats failed (%s): %s", resp.Status, bytes.TrimSpace(b))
	}

	var flows []api.FlowStat
	if err := json.NewDecoder(resp.Body).Decode(&flows); err != nil {
		return err
	}
	if len(flows) == 0 {
		fmt.Println("no flows")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VM\tPROTO\tSRC\tDST\tDIR\tPACKETS\tBYTES")
	for _, f := range flows {
		fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s:%d\t%s\t%d\t%d\n",
			f.VM, f.Proto, f.SrcIP, f.SrcPort, f.DstIP, f.DstPort, f.Dir, f.Packets, f.Bytes)
	}
	tw.Flush()
	return nil
}
