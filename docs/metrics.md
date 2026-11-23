# Metrics

Every binary exports its metrics over OTLP to the backend the `OTEL_*` variables name, and defines no
alarms. Each signal below has the threshold to alarm on, in whatever backend receives it. A counter's
threshold is its increase over the window named, 5 minutes where none is.

| Metric | Kind | From | Meaning | Alarm when |
|---|---|---|---|---|
| `queue_depth` | gauge | controller | items waiting in the environment's queue, from `work stats` | above 0 for 60 s while `collections` does not grow |
| `launches` | counter | controller | sandboxes launched for an item | `launches` minus `collections` above 5 over 10 minutes: sandboxes start and never collect |
| `collections` | counter | controller | items a sandbox collected through `GET /work-item` | see `queue_depth` |
| `handoff_timeouts` | counter | controller | items not collected within the handoff wait, whose sandbox the controller then tried to stop; a failed stop logs `sandbox did not collect its item` with the error | above 0 |
| `releases` | counter | controller | sandboxes and storage released: of a deleted or archived session, or with a retention set, of one idle past it | above 100 in 10 minutes: sessions are being deleted or archived in bulk, a retention just took effect, or agentd answers 404 for live ones |
| `reaped` | counter | controller, reaper | stale items stopped because their worker stopped heartbeating | above 0: a worker died without its own shutdown |
| `error_results` | counter | controller, reaper | error results posted for tool calls a dead worker owed | above 0, with `reaped` |

agentd's own `work stats` also reports `pending`, items handed out and not yet acked. The
controller acks only once a sandbox collects its item, up to the handoff wait, so `pending` is above
0 during every launch; alarm on it only when it stays above 0 longer than the handoff wait.

The proxy's `proxy_swaps`, `proxy_refusals` and `proxy_read_failures` are in the proxy's own docs;
alarm on `proxy_read_failures` above 0.

The service level the signals serve, as targets until the first e2e and AWS runs measure them: 95%
of queued items picked up within 15 s on EKS with a node free and within 60 s on Fargate.
