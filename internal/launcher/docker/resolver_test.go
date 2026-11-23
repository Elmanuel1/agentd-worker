package docker

import (
	"context"
	"os"
	"testing"

	"github.com/moby/moby/client"
)

// TestResolverTakesANewImage holds that a relay is kept while it runs the image the tool image
// names, under any reference, and replaced, with its address written to resolv.conf, once that
// tag names a rebuilt image.
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
	tag := "agentd-worker-resolver-test:ci"
	if _, err := first.cli.ImageTag(t.Context(), client.ImageTagOptions{Source: cfg.ToolImage, Target: tag}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.cli.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{}) })
	cfg.ToolImage = tag
	same := newLauncher(t, cfg)
	kept, _ := same.cli.ContainerInspect(t.Context(), name, client.ContainerInspectOptions{})
	rebuilt, err := same.cli.ContainerCommit(t.Context(), name, client.ContainerCommitOptions{Reference: tag, Changes: []string{"LABEL agentd.test=rebuilt"}})
	if err != nil {
		t.Fatal(err)
	}
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
		after.Container.Image != rebuilt.ID || string(conf) != "nameserver "+ip+"\noptions ndots:0\n" {
		t.Errorf("relay %s kept as %s, then %s from %s; resolv.conf %q", before.Container.ID, kept.Container.ID, after.Container.ID, after.Container.Image, conf)
	}
}
