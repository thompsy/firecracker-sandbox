//go:build ignore

// flowcount: a minimal tc/TCX eBPF program that counts packets and bytes per
// host interface and direction. Attached to a Firecracker VM's tap device, it
// yields that VM's ingress/egress traffic — the ifindex identifies the VM.
//
// This is the "hello world" step: no L3 parsing yet (5-tuple flow keys come
// next). Keeping it header-free makes the first verifier pass bulletproof.

#include <linux/bpf.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>

// key_t is the map key.
struct key_t {

  // ifindex is the id of the network interface.
  __u32 ifindex;

  // dir is the direction of travel of the packet. Ingress = 0, Egress = 1
  __u32 dir;
};

// val_t is the map value
struct val_t {

  // packets is the number packets we've seen.
  __u64 packets;

  // bytes is the number of bytes we've seen.
  __u64 bytes;
};

// flows is a hashmap, mapping from our key_t type to our val_t type.
struct {
  __uint(type, BPF_MAP_TYPE_HASH);
  __type(key, struct key_t);
  __type(value, struct val_t);
  __uint(max_entries, 1024);
} flows SEC(".maps");

// count updates the flows hashmap when called with a given socketBuffer and direction.
static __always_inline int count(struct __sk_buff *socketBuffer, __u32 dir)
{
  // Construct the map key.
  struct key_t key = {
    .ifindex = socketBuffer->ifindex,
    .dir = dir,
  };

  // Get the existing value (if it exists)
  struct val_t *v = bpf_map_lookup_elem(&flows, &key);
  if (v) {
    __sync_fetch_and_add(&v->packets, 1);
    __sync_fetch_and_add(&v->bytes, socketBuffer->len);
  } else {
    struct val_t init = {.packets = 1, .bytes = socketBuffer->len};
    bpf_map_update_elem(&flows, &key, &init, BPF_ANY);
  }

  // Since we're only observing the packet we want to let it continue.
  return TC_ACT_OK;
}

// count_ingress just calls count() for ingress packets.
SEC("tc")
int count_ingress(struct __sk_buff *socketBuffer)
{
  return count(socketBuffer, 0);
}

// count_egress just calls count() for egress packets.
SEC("tc")
int count_egress(struct __sk_buff *socketBuffer)
{
  return count(socketBuffer, 1);
}

char _license[] SEC("license") = "GPL";
