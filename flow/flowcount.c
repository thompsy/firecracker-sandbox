//go:build ignore

// flowcount: a minimal tc/TCX eBPF program that counts packets and bytes per
// host interface and direction. Attached to a Firecracker VM's tap device, it
// yields that VM's ingress/egress traffic — the ifindex identifies the VM.

#include <linux/bpf.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/in.h>
#include <bpf/bpf_endian.h>

// key_t is the map key.
struct key_t {

  // ifindex is the id of the network interface.
  __u32 ifindex;

  // dir is the direction of travel of the packet. Ingress = 0, Egress = 1
  __u32 dir;

  // Source and destination address.
  __be32 src_addr;
  __be32 dst_addr;

  // Source and destination port.
  __be16 src_port;
  __be16 dst_port;

  // Protocol
  __u8 proto;

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
  __uint(type, BPF_MAP_TYPE_LRU_HASH);
  __type(key, struct key_t);
  __type(value, struct val_t);
  __uint(max_entries, 65536);
} flows SEC(".maps");

// count updates the flows hashmap when called with a given socketBuffer and direction.
static __always_inline int count(struct __sk_buff *socketBuffer, __u32 dir) {


  void *data = (void *)(long)socketBuffer->data;
  void *data_end = (void *)(long)socketBuffer->data_end;

  // Extract the ethernet header
  struct ethhdr *eth = data;
  if ((void *)(eth + 1) > data_end){
    return TC_ACT_OK;
  }

  // Skip non-IP packets
  if (eth->h_proto != bpf_htons(ETH_P_IP)) {
    return TC_ACT_OK;
  }

  // Extract the IP header
  struct iphdr *ip = (void *)(eth + 1);
  if ((void *)(ip + 1) > data_end) {
      return TC_ACT_OK;
  }

  // A value less than 5 here means this packet it malformed
  if (ip->ihl < 5) {
    return TC_ACT_OK;
  }
  void *l4 = (char *)ip + ip->ihl * 4;

  // Skip anything that's not TCP or UDP
  if (ip->protocol != IPPROTO_TCP && ip->protocol != IPPROTO_UDP) {
    return TC_ACT_OK;
  }

  __be32 src_addr = ip->saddr;
  __be32 dst_addr = ip->daddr;
  __u8 proto = ip->protocol;

  __be16 src_port;
  __be16 dst_port;

  if (proto == IPPROTO_TCP) {
    // Extract the TCP header
    struct tcphdr *tcp = l4;
    if ((void *)(tcp + 1) > data_end) {
      return TC_ACT_OK;
    }

    src_port = tcp->source;
    dst_port = tcp->dest;
  } else if (proto == IPPROTO_UDP) {
    // Extract the UDP header
    struct udphdr *udp = l4;
    if ((void *)(udp + 1) > data_end) {
      return TC_ACT_OK;
    }

    src_port = udp->source;
    dst_port = udp->dest;
  }


  // Construct the map key.
  struct key_t key = {
    .ifindex = socketBuffer->ifindex,
    .dir = dir,
    .src_addr = src_addr,
    .dst_addr = dst_addr,
    .src_port = src_port,
    .dst_port = dst_port,
    .proto = proto,
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
int count_ingress(struct __sk_buff *socketBuffer) {
  return count(socketBuffer, 0);
}

// count_egress just calls count() for egress packets.
SEC("tc")
int count_egress(struct __sk_buff *socketBuffer) {
  return count(socketBuffer, 1);
}

char _license[] SEC("license") = "GPL";
