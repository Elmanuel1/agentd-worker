package docker

import (
	"os"
	"testing"

	"github.com/moby/moby/client"
)

// TestResolverTakesANewImage holds that a relay made from another image is replaced, and its
// address written to resolv.conf, while one from the same image is kept.
func TestResolverTakesANewImage(t *testing.T) {
	cfg := config(t)
	if cfg.Runtime == runc {
		t.Skip("under runc the adapter runs no relay")
	}
	first := newLauncher(t, cfg)
	name := "agentd-resolver-" + cfg.Network
	before, err := first.cli.ContainerInspect(t.Context(), name, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	image, err := first.cli.ImageInspect(t.Context(), cfg.ToolImage)
	if err != nil {
		t.Fatal(err)
	}
	same := newLauncher(t, cfg)
	kept, _ := same.cli.ContainerInspect(t.Context(), name, client.ContainerInspectOptions{})
	cfg.ToolImage = image.ID
	newer := newLauncher(t, cfg)
	after, err := newer.cli.ContainerInspect(t.Context(), name, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(newer.resolvConf())
	if err != nil {
		t.Fatal(err)
	}
	ip := after.Container.NetworkSettings.Networks[cfg.Network].IPAddress.String()
	if kept.Container.ID != before.Container.ID || after.Container.ID == before.Container.ID ||
		after.Container.Config.Image != image.ID || string(conf) != "nameserver "+ip+"\noptions ndots:0\n" {
		t.Errorf("relay %s kept as %s, then %s from %s; resolv.conf %q", before.Container.ID, kept.Container.ID, after.Container.ID, after.Container.Config.Image, conf)
	}
}
