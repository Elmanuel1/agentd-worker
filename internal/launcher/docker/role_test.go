package docker

import (
	"slices"
	"testing"
)

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

// TestCommands holds what each container runs: the worker Anthropic's script, as the chart runs
// it, since the image's own command is a shell; fetch and the sidecar agentd-worker's subcommands.
func TestCommands(t *testing.T) {
	l := &Launcher{cfg: Config{Runtime: runc, SandboxImage: "sandbox", ToolImage: "tool"}}
	want := map[role][]string{roleFetch: {"fetch"}, roleWorker: {"cma-sandbox-worker"}, roleSidecar: {"sidecar"}}
	for _, spec := range l.specs("sesn_1", "n1") {
		r := role(spec.Config.Labels[labelRole])
		if !slices.Equal(spec.Config.Cmd, want[r]) {
			t.Errorf("%s runs %q, want %q", r, spec.Config.Cmd, want[r])
		}
	}
}
