#!/bin/sh
# The cloud e2e on kind with Anthropic's chart (EK-1 to EK-5, PX-3, PX-4): a cloud session's curl
# reaches an allowed echo host with the real value and a disallowed one with the placeholder, a
# direct connection to a public IP is refused, the dispatcher serves the runtime queue, and the
# reaper Deployment answers a killed pod's owed call. It needs the iron-proxy extension's image,
# which another change builds, so it is pending until then.
set -eu
echo "e2e/kind.sh is pending: it needs the iron-proxy extension's image" >&2
exit 1
