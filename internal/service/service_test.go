package service

import (
	"os"
	"strings"
	"testing"
)

func TestEnsureMetricsArgInsertsBeforeTunnel(t *testing.T) {
	got := EnsureMetricsArg([]string{"/usr/local/bin/cloudflared", "--config", "/etc/cloudflared/config.yml", "tunnel", "run"}, "127.0.0.1:20242")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--metrics 127.0.0.1:20242 tunnel run") {
		t.Fatalf("metrics was not inserted before tunnel: %q", joined)
	}
}

func TestEnsureMetricsArgReplacesExisting(t *testing.T) {
	got := EnsureMetricsArg([]string{"cloudflared", "--metrics", "127.0.0.1:20241", "tunnel", "run"}, "127.0.0.1:20242")
	if MetricsArg(got) != "127.0.0.1:20242" {
		t.Fatalf("metrics arg=%q, want replacement", MetricsArg(got))
	}
}

func TestParseSystemdExecStart(t *testing.T) {
	unit := "[Service]\nExecStart=/usr/local/bin/cloudflared --config /etc/cloudflared/config.yml tunnel run\n"
	got := ParseSystemdExecStart(unit)
	if len(got) != 5 || got[0] != "/usr/local/bin/cloudflared" || got[2] != "/etc/cloudflared/config.yml" {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestRenderSystemdUnitIncludesMetrics(t *testing.T) {
	unit := RenderSystemdUnit("cfpick-cloudflared-blue", []string{"cloudflared", "--metrics", "127.0.0.1:20242", "tunnel", "run"})
	if !strings.Contains(unit, "ExecStart=cloudflared --metrics 127.0.0.1:20242 tunnel run") {
		t.Fatalf("unit missing metrics args:\n%s", unit)
	}
}

func TestRenderLaunchdPlistIncludesProgramArguments(t *testing.T) {
	plist := RenderLaunchdPlist("com.kayphoon.cfpick.cloudflared-blue", []string{"cloudflared", "--metrics", "127.0.0.1:20242", "tunnel", "run"})
	if !strings.Contains(plist, "<string>com.kayphoon.cfpick.cloudflared-blue</string>") || !strings.Contains(plist, "<string>127.0.0.1:20242</string>") {
		t.Fatalf("plist missing label or metrics:\n%s", plist)
	}
}

func TestParseLaunchdProgramArguments(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cloudflared-*.plist")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.WriteString(RenderLaunchdPlist("com.example.cloudflared", []string{"cloudflared", "--config", "/etc/cloudflared/config.yml", "tunnel", "run"}))
	got, err := ParseLaunchdProgramArguments(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0] != "cloudflared" || got[2] != "/etc/cloudflared/config.yml" {
		t.Fatalf("unexpected args: %#v", got)
	}
}
