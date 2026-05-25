package install

import (
	"context"
	"strings"
	"testing"

	"github.com/kayphoon/tunnelflux/internal/config"
	"github.com/kayphoon/tunnelflux/internal/probe"
	"github.com/kayphoon/tunnelflux/internal/switcher"
)

func TestRenderUnitUsesDiscoveredCloudflaredService(t *testing.T) {
	unit := RenderUnit("/usr/bin/tf", "/etc/tunnelflux/config.json", "cloudflared-custom")
	if !strings.Contains(unit, "After=network-online.target cloudflared-custom.service") {
		t.Fatalf("unit did not use discovered service:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/usr/bin/tf run --config /etc/tunnelflux/config.json") {
		t.Fatalf("unit did not render ExecStart:\n%s", unit)
	}
}

func TestProbeConfigForInstallCapsHeavyDefaults(t *testing.T) {
	cfg := config.Default()
	got := probeConfigForInstall(cfg)
	if got.Edge.MaxCandidates != installProbeMaxCandidates {
		t.Fatalf("max candidates = %d, want %d", got.Edge.MaxCandidates, installProbeMaxCandidates)
	}
	if got.Edge.ProbeRounds != installProbeRounds {
		t.Fatalf("probe rounds = %d, want %d", got.Edge.ProbeRounds, installProbeRounds)
	}
	if got.Edge.Concurrency != installProbeConcurrency {
		t.Fatalf("concurrency = %d, want %d", got.Edge.Concurrency, installProbeConcurrency)
	}
	if got.Edge.ProbeTimeout != installProbeTimeout {
		t.Fatalf("probe timeout = %s, want %s", got.Edge.ProbeTimeout, installProbeTimeout)
	}
	if cfg.Edge.MaxCandidates != config.Default().Edge.MaxCandidates {
		t.Fatalf("probeConfigForInstall mutated input config")
	}
}

func TestRenderLaunchdPlistUsesTunnelFluxRun(t *testing.T) {
	plist := RenderLaunchdPlist("/usr/local/bin/tf", "/etc/tunnelflux/config.json")
	if !strings.Contains(plist, "<string>com.kayphoon.tunnelflux</string>") {
		t.Fatalf("plist missing label:\n%s", plist)
	}
	if !strings.Contains(plist, "<string>/usr/local/bin/tf</string>") || !strings.Contains(plist, "<string>run</string>") {
		t.Fatalf("plist missing tf run args:\n%s", plist)
	}
}

func TestApplyInitialPinUsesProbeTopIPs(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.DryRun = false
	cfg.Cloudflared.Protocol = config.ProtocolAuto

	pr := probe.Report{
		EffectiveProtocol: config.ProtocolQUIC,
		Top: []probe.Result{
			{IP: "198.41.200.1", OK: 2},
			{IP: "198.41.200.2", OK: 2},
		},
	}

	var gotIPs []string
	var gotProtocol string
	var gotDryRun bool
	res, err := applyInitialPin(context.Background(), cfg, pr, func(_ context.Context, gotCfg config.Config, ips []string, protocol string) (switcher.Result, error) {
		gotIPs = append([]string(nil), ips...)
		gotProtocol = protocol
		gotDryRun = gotCfg.Runtime.DryRun
		return switcher.Result{Applied: true, Strategy: gotCfg.Switching.Strategy, Protocol: protocol}, nil
	})
	if err != nil {
		t.Fatalf("applyInitialPin returned error: %v", err)
	}
	if !res.Applied {
		t.Fatalf("applyInitialPin did not report applied result")
	}
	if strings.Join(gotIPs, ",") != "198.41.200.1,198.41.200.2" {
		t.Fatalf("ips = %v, want probe top IP order", gotIPs)
	}
	if gotProtocol != config.ProtocolQUIC {
		t.Fatalf("protocol = %s, want %s", gotProtocol, config.ProtocolQUIC)
	}
	if gotDryRun {
		t.Fatalf("initial pin should run with dry-run disabled")
	}
}
