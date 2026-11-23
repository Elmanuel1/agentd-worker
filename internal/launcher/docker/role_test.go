package docker

import (
	"maps"
	"slices"
	"testing"

	"github.com/Elmanuel1/agentd-worker/internal/launcher"
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
	l := &Launcher{cfg: Config{Runtime: runc, Sandbox: launcher.Sandbox{SandboxImage: "sandbox", ToolImage: "tool"}}}
	want := map[role][]string{roleFetch: {"fetch"}, roleWorker: {"cma-sandbox-worker"}, roleSidecar: {"sidecar"}}
	got := map[role][]string{}
	for _, spec := range l.specs("sesn_1", "n1") {
		got[role(spec.Config.Labels[labelRole])] = spec.Config.Cmd
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("containers run %q, want %q", got, want)
	}
}
