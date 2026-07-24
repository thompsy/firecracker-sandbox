// Package flow loads the flowcount eBPF program and reads per-VM traffic
// counters from its map. The eBPF object is generated from flowcount.c by
// bpf2go (run `go generate ./flow`).
package flow

// The trailing clang flags add the arch asm/ headers (x86_64 only, matching the
// rest of this project) so `-target bpf` can find <asm/types.h>.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type key_t -type val_t flowcount flowcount.c -- -I/usr/include/x86_64-linux-gnu
