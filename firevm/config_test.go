package firevm

import "testing"

func TestScheme(t *testing.T) {
	cases := []struct {
		id                 int
		ip, tap, mac, name string
	}{
		{0, "172.16.0.2", "fc-tap0", "06:00:AC:10:00:02", "fc-vm0"},
		{1, "172.16.0.3", "fc-tap1", "06:00:AC:10:01:02", "fc-vm1"},
		{15, "172.16.0.17", "fc-tap15", "06:00:AC:10:0f:02", "fc-vm15"},
	}
	for _, c := range cases {
		if got := guestIP(c.id); got != c.ip {
			t.Errorf("GuestIP(%d) = %q, want %q", c.id, got, c.ip)
		}
		if got := TapName(c.id); got != c.tap {
			t.Errorf("TapName(%d) = %q, want %q", c.id, got, c.tap)
		}
		if got := mac(c.id); got != c.mac {
			t.Errorf("MAC(%d) = %q, want %q", c.id, got, c.mac)
		}
		if got := VMName(c.id); got != c.name {
			t.Errorf("VMName(%d) = %q, want %q", c.id, got, c.name)
		}
	}
}
