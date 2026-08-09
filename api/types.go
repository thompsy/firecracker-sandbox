// Package api holds the request/response types shared by firevm and firevmd.
package api

// LaunchRequest contains the data needed to launch a new VM.
type LaunchRequest struct {
	ID  int    `json:"id"`
	Cmd string `json:"cmd"`
}

// FlowStat is one correlated traffic flow in the GET /stats response: an eBPF
// 5-tuple counter attributed to the VM it belongs to (via its tap's ifindex).
type FlowStat struct {
	VMID    int    `json:"vm_id"`
	VM      string `json:"vm"`  // VM name, e.g. "fc-vm0"
	Dir     string `json:"dir"` // "in" or "out"
	SrcIP   string `json:"src_ip"`
	SrcPort uint16 `json:"src_port"`
	DstIP   string `json:"dst_ip"`
	DstPort uint16 `json:"dst_port"`
	Proto   string `json:"proto"` // "TCP", "UDP", or "UNK"
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}
