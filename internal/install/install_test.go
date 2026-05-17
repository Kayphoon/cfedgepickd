package install

import (
	"strings"
	"testing"

	"github.com/kayphoon/cfpick/internal/config"
)

func TestRenderUnitUsesDiscoveredCloudflaredService(t *testing.T) {
	unit := RenderUnit("/usr/bin/cfpick", "/etc/cfpick/config.json", "cloudflared-custom")
	if !strings.Contains(unit, "After=network-online.target cloudflared-custom.service") {
		t.Fatalf("unit did not use discovered service:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/usr/bin/cfpick run --config /etc/cfpick/config.json") {
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

func TestRenderLaunchdPlistUsesCfpickRun(t *testing.T) {
	plist := RenderLaunchdPlist("/usr/local/bin/cfpick", "/etc/cfpick/config.json")
	if !strings.Contains(plist, "<string>com.kayphoon.cfpick</string>") {
		t.Fatalf("plist missing label:\n%s", plist)
	}
	if !strings.Contains(plist, "<string>/usr/local/bin/cfpick</string>") || !strings.Contains(plist, "<string>run</string>") {
		t.Fatalf("plist missing cfpick run args:\n%s", plist)
	}
}
