package docker

import "testing"

// TestRoles holds each container role's stop grace and whether it holds the sandbox's address.
func TestRoles(t *testing.T) {
	cases := []struct {
		r           role
		grace       int
		addressable bool
	}{
		{roleFetch, 30, true},
		{roleWorker, 60, true},
		{roleSidecar, 30, false},
		{roleResolver, 30, false},
	}
	for _, c := range cases {
		if c.r.grace() != c.grace || c.r.addressable() != c.addressable {
			t.Errorf("%s: grace %d, addressable %v", c.r, c.r.grace(), c.r.addressable())
		}
	}
}
