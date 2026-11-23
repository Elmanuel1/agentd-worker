package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/launchertest"
)

// The suite needs a Docker engine and the two images, so it runs in CI, where
// AGENTD_TEST_DOCKER names the network and the images are built first.
func config(t *testing.T) Config {
	t.Helper()
	network := os.Getenv("AGENTD_TEST_DOCKER")
	if network == "" {
		t.Skip("AGENTD_TEST_DOCKER is not set; the Docker adapter suite runs in CI")
	}
	return Config{
		Network:      network,
		Runtime:      envOr("AGENTD_TEST_DOCKER_RUNTIME", "runc"),
		SandboxImage: os.Getenv("AGENTD_TEST_SANDBOX_IMAGE"),
		ToolImage:    os.Getenv("AGENTD_TEST_TOOL_IMAGE"),
		// No controller answers here, so fetch keeps retrying and the sandbox stays running.
		HandoffURL:  "https://127.0.0.1:1",
		HandoffCA:   testCA(t),
		HandoffWait: 90 * time.Second,
		StateDir:    t.TempDir(),
	}
}

func newLauncher(t *testing.T, cfg Config) *Launcher {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := New(ctx, cli, cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); l.Close(); cli.Close() })
	return l
}

// TestSuite holds DK-1 and DK-3.
func TestSuite(t *testing.T) {
	cfg := config(t)
	launchertest.Run(t, func(t *testing.T) (launchertest.Subject, launchertest.Count) {
		l := newLauncher(t, cfg)
		count := func(ctx context.Context, s ids.SessionID) (int, error) {
			cs, err := l.containers(ctx, s)
			return len(slices.DeleteFunc(cs, func(c container.Summary) bool { return c.Labels[labelRole] != roleFetch })), err
		}
		return l, count
	})
}

// TestRefusesWithoutRuntime holds DK-2.
func TestRefusesWithoutRuntime(t *testing.T) {
	cfg := config(t)
	cfg.Runtime = "no-such-runtime"
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := New(t.Context(), cli, cfg, slog.Default()); err == nil || !strings.Contains(err.Error(), "lacks the sandbox runtime") {
		t.Errorf("New without the runtime: %v", err)
	}
}

// TestIsolation holds DK-4: no socket, host path, host or shared namespace, or added capability.
func TestIsolation(t *testing.T) {
	cfg := config(t)
	l := newLauncher(t, cfg)
	session := ids.SessionID("sesn_isolation")
	if err := l.Launch(t.Context(), session, ids.NewNonce()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Release(context.Background(), session) })
	containers, err := l.containers(t.Context(), session)
	if err != nil || len(containers) != 3 {
		t.Fatalf("%d containers, %v", len(containers), err)
	}
	for _, c := range containers {
		res, err := l.cli.ContainerInspect(t.Context(), c.ID, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		hc := res.Container.HostConfig
		role := c.Labels[labelRole]
		if len(hc.Binds) > 0 || hc.Privileged || len(hc.CapAdd) > 0 || hc.PidMode != "" || hc.IpcMode.IsHost() || hc.IpcMode.IsShareable() || hc.UsernsMode.IsHost() || hc.NetworkMode.IsHost() {
			t.Errorf("%s: host config %+v", role, hc)
		}
		if hc.Runtime != cfg.Runtime {
			t.Errorf("%s runs under %q", role, hc.Runtime)
		}
		for _, m := range res.Container.Mounts {
			resolv := m.Type == "bind" && m.Destination == "/etc/resolv.conf" && !m.RW && cfg.Runtime != runc
			if (m.Type != "volume" && !resolv) || strings.Contains(m.Source, "docker.sock") {
				t.Errorf("%s mounts %+v", role, m)
			}
		}
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// testCA is a throwaway certificate, so fetch starts and retries rather than refusing its settings.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// TestAdoptsAfterRestart holds SD-1 and DK-1 across a controller restart: a new adapter watches
// a sandbox an earlier one launched, and removes it when its fetch gives up.
func TestAdoptsAfterRestart(t *testing.T) {
	cfg := config(t)
	cfg.HandoffWait = 5 * time.Second
	session := ids.SessionID("sesn_adopt")
	first := newLauncher(t, cfg)
	if err := first.Launch(t.Context(), session, ids.NewNonce()); err != nil {
		t.Fatal(err)
	}
	first.Close()
	waitRemoved(t, newLauncher(t, cfg), session, "the adopted sandbox was not removed after its fetch gave up")
}

// TestRemovesAnUnfetchedSandbox holds DK-1 across a restart between creating a sandbox and
// starting its fetch: the new adapter removes it rather than start its worker with no item.
func TestRemovesAnUnfetchedSandbox(t *testing.T) {
	cfg := config(t)
	session := ids.SessionID("sesn_unfetched")
	first := newLauncher(t, cfg)
	for _, spec := range first.specs(session, ids.NewNonce()) {
		if _, err := first.cli.ContainerCreate(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
	}
	first.Close()
	waitRemoved(t, newLauncher(t, cfg), session, "a sandbox whose fetch never started was kept")
}

func waitRemoved(t *testing.T, l *Launcher, session ids.SessionID, failure string) {
	t.Helper()
	t.Cleanup(func() { l.Release(context.Background(), session) })
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		cs, err := l.containers(t.Context(), session)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) == 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Error(failure)
}
