package flow

import (
	"fmt"
	"net"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

// Monitor holds the loaded flowcount program/map and its TCX attachments.
type Monitor struct {
	objs  flowcountObjects
	links []link.Link
}

// Sample is a per-interface, per-direction traffic counter.
type Sample struct {
	Ifindex uint32
	Egress  bool
	SrcAddr uint32
	SrcPort uint16
	DstAddr uint32
	DstPort uint16
	Proto   uint8
	Packets uint64
	Bytes   uint64
}

// Load loads the flowcount eBPF objects into the kernel. Requires root/CAP_BPF.
func Load() (*Monitor, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w", err)
	}
	m := &Monitor{}
	if err := loadFlowcountObjects(&m.objs, nil); err != nil {
		return nil, fmt.Errorf("load bpf objects: %w", err)
	}
	return m, nil
}

// Attach hooks the counter onto both directions of the named interface via TCX.
func (m *Monitor) Attach(ifname string) error {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return fmt.Errorf("interface %q: %w", ifname, err)
	}
	for _, a := range []struct {
		prog   *ebpf.Program
		attach ebpf.AttachType
	}{
		{m.objs.CountIngress, ebpf.AttachTCXIngress},
		{m.objs.CountEgress, ebpf.AttachTCXEgress},
	} {
		l, err := link.AttachTCX(link.TCXOptions{
			Interface: iface.Index,
			Program:   a.prog,
			Attach:    a.attach,
		})
		if err != nil {
			return fmt.Errorf("attach TCX to %s: %w", ifname, err)
		}
		m.links = append(m.links, l)
	}
	return nil
}

// Stats returns the current per-interface, per-direction counters.
func (m *Monitor) Stats() ([]Sample, error) {
	var (
		key     flowcountKeyT
		val     flowcountValT
		samples []Sample
	)
	it := m.objs.Flows.Iterate()
	for it.Next(&key, &val) {
		samples = append(samples, Sample{
			Ifindex: key.Ifindex,
			Egress:  key.Dir == 1,
			SrcAddr: key.SrcAddr,
			SrcPort: key.SrcPort,
			DstAddr: key.DstAddr,
			DstPort: key.DstPort,
			Proto:   key.Proto,
			Packets: val.Packets,
			Bytes:   val.Bytes,
		})
	}
	return samples, it.Err()
}

// Close detaches the programs and releases the objects.
func (m *Monitor) Close() error {
	for _, l := range m.links {
		_ = l.Close()
	}
	return m.objs.Close()
}
