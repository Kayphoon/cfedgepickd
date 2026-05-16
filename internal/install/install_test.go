package install

import (
	"strings"
	"testing"
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
