# Metrics

Every binary exports its metrics over OTLP to the backend the `OTEL_*` variables name, and defines no
alarms. Each signal below has the threshold to alarm on, in whatever backend receives it.

| Metric | Kind | From | Meaning | Alarm when |
|---|---|---|---|---|
| `queue_depth` | gauge | controller | items waiting in the environment's queue, from `work stats` | above 0 for 60 s while `collections` does not grow |
| `launches` | counter | controller | sandboxes launched for an item | `launches` minus `collections` above 5 over 10 minutes: sandboxes start and never collect |
| `collections` | counter | controller | items a sandbox collected through `GET /work-item` | see `queue_depth` |
| `handoff_timeouts` | counter | controller | items not collected within the handoff wait, whose sandbox was stopped | above 0 |
| `releases` | counter | controller | sandboxes released because agentd no longer has their session | above 100 in 10 minutes: sessions are being deleted in bulk, or agentd answers 404 for live ones |
| `reaped` | counter | controller, reaper | stale items stopped because their worker stopped heartbeating | above 0: a worker died without its own shutdown |
| `error_results` | counter | controller, reaper | error results posted for tool calls a dead worker owed | above 0, with `reaped` |

The proxy's `proxy_swaps`, `proxy_refusals` and `proxy_read_failures` are in the proxy's own docs;
alarm on `proxy_read_failures` above 0.

The service level the signals serve: 95% of queued items picked up within 15 s on EKS with a node
free and within 60 s on Fargate, measured from the first e2e and AWS runs.
