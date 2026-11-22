# Adding a launcher adapter

A launcher runs sandboxes on one backend. The controller declares what it needs in
`internal/dispatch/ports.go`; an adapter is a package under `internal/launcher/` with a type that
has these methods, returned as a concrete type from its constructor:

| Port | Method | Contract |
|---|---|---|
| `Launcher` | `Launch(ctx, session, nonce)` | Replace whatever the session had with a fresh sandbox on the session's kept volume. Its fetch container gets the nonce, the handoff URL and CA; its worker starts only after fetch exits 0; its sidecar is stopped with SIGTERM when the worker exits. |
| `Launcher` | `List(ctx)` | Each session the backend holds a sandbox or a volume for, and whether its sandbox runs. |
| `Launcher` | `Stop(ctx, session)` | Stop the session's sandbox and keep its volume. |
| `Launcher` | `Release(ctx, session)` | Stop the sandbox and delete everything the backend keeps for the session. |
| `Locator` | `Address(ctx, session)` | The running sandbox's address, which the controller compares with the caller of `GET /work-item`. |

Every call is safe to repeat and safe on a session the backend has never seen. A sandbox holds no
environment or runtime key, no host path, no host namespace, no added capability and no cloud
credential that grants anything.

Then pass the shared suite, `internal/launcher/launchertest`, against the backend or a faithful
fake of its API. The suite also takes a `Count` that reads, from the backend itself, how many
sandboxes it holds for a session, so a second sandbox that `List` would fold into one still shows:

```go
func TestSuite(t *testing.T) {
	launchertest.Run(t, func(t *testing.T) (launchertest.Subject, launchertest.Count) {
		b := mybackend.New(...)
		return b, func(ctx context.Context, s ids.SessionID) (int, error) { return countTasks(ctx, s) }
	})
}
```

Finally wire the adapter in `cmd/agentd-worker/launchers.go` under a new `AGENTD_WORKER_LAUNCHER`
value, reading its settings from environment variables.
