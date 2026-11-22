#!/bin/sh
# Installs gVisor's runsc on a CI runner and registers it with Docker as "runsc", pinned to
# release 20261116 with the sha512 read from its release on 2026-11-22. Bump with nodepool.yaml.
set -eu
release=20261116
case "$(uname -m)" in
x86_64) sha512=c87d792d5842efb70c38ee822124cfe0821a547715e7abcf3f2b14997cd02784edaa0b91e238ec5c27a25a1b9e00bf98113398eb62a0778d8d20d4e222f8b0e0 ;;
aarch64) sha512=a64ce2d5ba6825a0a6459ee6a9ce40164749c2d276a945b885673988c4e545e66b2f4ef3a08e068a26cca676061c8c5e8ba982156feb786f0eab995f5b8b854d ;;
*) echo "no pinned gVisor for $(uname -m)" >&2; exit 1 ;;
esac
dir="$(mktemp -d)"
curl -fsSLo "$dir/gvisor.tar.bz2" "https://storage.googleapis.com/gvisor/releases/release/$release/$(uname -m)/gvisor.tar.bz2"
echo "$sha512  $dir/gvisor.tar.bz2" | sha512sum -c -
tar -xjf "$dir/gvisor.tar.bz2" -C "$dir" runsc containerd-shim-runsc-v1
sudo install -m 0755 "$dir/runsc" "$dir/containerd-shim-runsc-v1" /usr/local/bin/
sudo /usr/local/bin/runsc install
# Docker reloads its runtimes on SIGHUP, so the job's service containers keep running.
sudo systemctl reload docker
docker info --format '{{json .Runtimes}}' | grep -q '"runsc"'
