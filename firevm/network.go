package firevm

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// SetupTap creates the VM's tap device if needed, enslaves it to the shared bridge and brings it
// up.
func SetupTap(id int) error {
	br, err := netlink.LinkByName(Bridge)
	if err != nil {
		return fmt.Errorf("bridge %q not found (run: make net-up): %w", Bridge, err)
	}

	name := TapName(id)
	tap, err := netlink.LinkByName(name)
	if err != nil {
		tap = &netlink.Tuntap{
			LinkAttrs: netlink.LinkAttrs{Name: name},
			Mode:      netlink.TUNTAP_MODE_TAP,
		}
		if err := netlink.LinkAdd(tap); err != nil {
			return fmt.Errorf("create tap %q: %w", name, err)
		}
	}
	if err := netlink.LinkSetMaster(tap, br); err != nil {
		return fmt.Errorf("enslave %q to %q: %w", name, Bridge, err)
	}
	if err := netlink.LinkSetUp(tap); err != nil {
		return fmt.Errorf("bring %q up: %w", name, err)
	}
	return nil
}

// DelTap removes the VM's tap device. It is a no-op if the tap is already gone.
func DelTap(id int) error {
	tap, err := netlink.LinkByName(TapName(id))
	if err != nil {
		return nil
	}
	return netlink.LinkDel(tap)
}
