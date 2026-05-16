# cfpick

`cfpick` is a small control-plane daemon and CLI for `cloudflared` Tunnel edge IP
selection.

It does not proxy traffic. It probes Cloudflare edge candidates, observes `cloudflared`
health and idle windows, updates `/etc/hosts`, restarts `cloudflared`, and rolls back
when the tunnel does not recover.

## Scope

Supported in v1:

- Linux with systemd
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

- OpenWrt / non-systemd service managers
- Docker-only cloudflared management
- DNS server integration instead of `/etc/hosts`

## Commands

```bash
cfpick status
cfpick status --metric error_delta --since 24h
cfpick discover
cfpick probe --protocol auto
cfpick install --dry-run --protocol auto
cfpick once --config /etc/cfpick/config.json
cfpick run --config /etc/cfpick/config.json
```

`install --dry-run` only prints discovered config, probe output, and the unit that
would be installed. `install --apply` writes `/etc/cfpick/config.json` and the
`cfpick.service` systemd unit.

## History And Graphs

Each daemon cycle appends one JSONL record to `/var/lib/cfpick/history.jsonl`.
The file path is configurable through `runtime.history_file`.

Use `cfpick status` to show a terminal dashboard. It renders current health,
active edge sockets, the latest sample, and a line chart for the selected metric:

```bash
cfpick status --metric rtt --since 24h
cfpick status --metric request_delta --since 24h
cfpick status --metric error_delta --since 24h
cfpick status --metric ready --since 7d --width 100 --height 16
```

Supported metrics are `rtt`, `ready`, `ha`, `concurrent`, `requests`,
`request_delta`, `errors`, `error_delta`, `degraded`, and `idle`.

## Build

```bash
make test
make dist
```

`make dist` builds static Linux binaries for `linux/amd64` and `linux/arm64`:

```text
dist/cfpick-linux-amd64.tar.gz
dist/cfpick-linux-arm64.tar.gz
```

## Safety

The daemon only switches when:

- a better TopN set exists,
- current connections look degraded,
- cooldown is not active,
- and the configured idle window is satisfied.

Emergency switching can bypass the idle requirement when `readyConnections < 2`.

All hosts/config writes are backed up before restart. If `cloudflared` does not reach
`readyConnections >= 2` before timeout, the daemon restores the backup and restarts
`cloudflared` again.
