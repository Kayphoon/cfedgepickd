# cfpick

`cfpick` is a small control-plane daemon and CLI for `cloudflared` Tunnel edge IP
selection.

It does not proxy traffic. It probes Cloudflare edge candidates, observes `cloudflared`
health and idle windows, updates `/etc/hosts`, hot-switches between blue/green
`cloudflared` slots, and rolls back when the new slot does not become healthy.

## Scope

Supported in v1:

- Linux with systemd
- macOS with system LaunchDaemon
- `cloudflared tunnel run`
- `/etc/hosts` based edge hostname pinning
- `auto`, `quic`, and `http2` protocol modes
- QUIC probing first, with HTTP/2 TCP fallback in `auto`

QUIC probing uses the same edge TLS server name as `cloudflared` itself:
`quic.cftunnel.com`. The `region*.v2.argotunnel.com` names are the edge hostnames
that `/etc/hosts` pins, not the QUIC TLS SNI.

Cloudflare Tunnel QUIC edge presents a Cloudflare Origin certificate. The probe
therefore validates the expected `argotunnel` ALPN and Cloudflare Origin certificate
shape instead of requiring a public-web CA chain.

When configured with `protocol: auto`, `cfpick` may use QUIC probes to rank IPs,
but it keeps `cloudflared` configured as `auto` so production traffic can fall back to
HTTP/2 if UDP/QUIC breaks later.

Not supported in v1:

- OpenWrt and other service managers
- Docker-only cloudflared management
- DNS server integration instead of `/etc/hosts`

## Install

Dry-run from the latest GitHub Release:

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sh -s -- --dry-run
```

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --apply --protocol auto
```

Install and start the daemon immediately:

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --apply --protocol auto --start
```

Pin a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --apply --version v0.2.12
```

The installer detects Linux/macOS and `amd64`/`arm64`, downloads the matching
release archive, verifies `checksums.txt` when available, installs the binaries,
writes `/etc/cfpick/config.json`, and writes the platform service definition. It
does not start the daemon unless `--start` is passed.

## Commands

```bash
cfpick status
cfpick status --metric error_delta --since 24h
cfpick discover
cfpick probe --protocol auto
cfpick install --dry-run --protocol auto
cfpick once --config /etc/cfpick/config.json
cfpick switch --config /etc/cfpick/config.json
cfpick switch --apply --config /etc/cfpick/config.json
cfpick switch --apply --ips 198.41.200.227,198.41.200.132 --config /etc/cfpick/config.json
cfpick switch --apply --mode restart --config /etc/cfpick/config.json
cfpick run --config /etc/cfpick/config.json
```

`install --dry-run` only prints discovered config, probe output, and the unit that
would be installed. `install --apply` writes `/etc/cfpick/config.json` and the
platform service unit or plist.

`switch` is the manual replacement command. Without `--apply` it probes and prints
the planned blue/green switch. With `--apply` it writes the selected IPs, starts
the inactive slot, waits for `readyConnections >= 2`, gracefully stops the old
active slot, and rolls back if the new slot does not become healthy. Passing
`--ips` skips probing and applies those IPs directly. `--mode restart` keeps the
older restart-based behavior.

## History And Graphs

Each daemon cycle appends one JSONL record to `/var/lib/cfpick/history.jsonl`.
By default the daemon samples every 5 minutes, controlled by
`switching.probe_interval_seconds`. The file path is configurable through
`runtime.history_file`.

History retention defaults to 30 days through `runtime.history_retention_days`.
Records older than that are pruned after each successful append. Set the value to
a negative number to disable pruning.

Use `cfpick status` to show a terminal dashboard. It renders current health,
cloudflared performance, active edge sockets, the latest sample, and a time-ordered
line chart. The default chart overlays request rate and error rate with different
colors:

```bash
cfpick status --metric request_rate --since 24h
cfpick status --metric response_5xx_delta --since 24h
cfpick status --metric rss_mb --since 24h
cfpick status --metric request_delta --since 24h
cfpick status --metric error_delta --since 24h
cfpick status --metric rtt --since 24h
cfpick status --metric ready --since 7d --width 100 --height 16
```

Supported metrics include `request_rate`, `request_delta`, `error_rate`,
`error_delta`, `response_5xx_delta`, `response_5xx_rate`, `rss_mb`, `heap_mb`,
`goroutines`, `cpu_percent`, `network_rx_rate`, `network_tx_rate`, `rtt`, `ready`,
`ha`, `concurrent`, `degraded`, and `idle`.

## Build And Release

```bash
make test
make dist VERSION=v0.2.12
```

`make dist` builds static binaries for Linux and macOS:

```text
dist/cfpick-linux-amd64.tar.gz
dist/cfpick-linux-arm64.tar.gz
dist/cfpick-darwin-amd64.tar.gz
dist/cfpick-darwin-arm64.tar.gz
dist/checksums.txt
dist/install.sh
```

Each archive contains `cfpick`, `cfedgepickd`, `cfedgepickctl`, `install.sh`,
both systemd service files, and both example config files. `cfpick` remains the
default compatibility install entrypoint, while the `cfedgepickd` daemon and
`cfedgepickctl` helper are included in the same package.

GitHub Actions runs the same release build for pull requests and pushes to
`main`, uploading short-lived artifacts for inspection. Pushing a version tag
creates a GitHub Release and uploads the platform archives, `checksums.txt`,
and `install.sh`:

```bash
git tag v0.2.12
git push origin v0.2.12
```

Tag builds embed the tag name in `cfpick version`, `cfedgepickd version`, and
`cfedgepickctl version`. Non-tag CI builds embed the branch/ref name plus the
short commit SHA.

## Safety

The daemon only switches when:

- a better TopN set exists,
- current connections look degraded,
- cooldown is not active,
- and the configured idle window is satisfied.

Emergency switching can bypass the idle requirement when `readyConnections < 2`.
Manual `cfpick switch --apply` intentionally bypasses degraded, cooldown, and idle
gates because it is an explicit operator action. It defaults to blue/green hot
switching; `--mode restart` is available for the older restart path.

All hosts/config writes are backed up before a switch. If the inactive slot does not
reach `readyConnections >= 2` before timeout, cfpick stops it, restores the backup,
and keeps the old active slot running.
