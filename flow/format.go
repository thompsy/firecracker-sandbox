package flow

import (
	"encoding/binary"
	"net"
)

// Port converts a __be16 port field from the flow map into a host-order port number.
func Port(v uint16) uint16 {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], v) // recover the on-wire (network) bytes
	return binary.BigEndian.Uint16(b[:])   // read them big-endian => host value
}

// IPv4 converts a __be32 address field from the flow map into a net.IP.
func IPv4(v uint32) net.IP {
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], v) // network-order bytes...
	return net.IP(b[:])                    // ...which is exactly what net.IP wants
}

// Proto maps an IP protocol number to a short name.
func Proto(p uint8) string {
	if p == 6 {
		return "TCP"
	}
	if p == 17 {
		return "UDP"
	}
	return "UNK"
}
