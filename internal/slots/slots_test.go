package slots

import (
	"net"
	"testing"

	"github.com/kayphoon/tunnelflux/internal/config"
)

func TestDefaultStateUsesConfigMetricsForGreen(t *testing.T) {
	cfg := config.Default()
	cfg.Cloudflared.MetricsURL = "http://127.0.0.1:20245/metrics"
	cfg.Cloudflared.ReadyURL = "http://127.0.0.1:20245/ready"
	st := DefaultState(cfg)
	if st.Active != Green || st.Green.MetricsURL != cfg.Cloudflared.MetricsURL {
		t.Fatalf("unexpected default state: %+v", st)
	}
}

func TestFindFreeMetricsAddrSkipsExcludedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	addr, err := FindFreeMetricsAddr("127.0.0.1", port, port+1, map[int]bool{port: true})
	if err != nil {
		t.Fatal(err)
	}
	if PortFromURL("http://"+addr+"/metrics") == port {
		t.Fatalf("selected excluded port: %s", addr)
	}
}

func TestFirstMetricsAddrSkipsExcludedPortWithoutListening(t *testing.T) {
	addr, err := FirstMetricsAddr("127.0.0.1", 20241, 20243, map[int]bool{20241: true})
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:20242" {
		t.Fatalf("addr=%q", addr)
	}
}

func TestStateActiveAndInactiveSlots(t *testing.T) {
	st := DefaultState(config.Default())
	st.SetActive(Blue)
	if st.ActiveSlot().Name != Blue || st.InactiveSlot().Name != Green {
		t.Fatalf("unexpected active/inactive: %+v", st)
	}
}

func TestBlueServiceNameIsPlatformSpecific(t *testing.T) {
	if got := BlueServiceName("linux"); got != "tunnelflux-cloudflared-blue" {
		t.Fatalf("linux blue service=%q", got)
	}
	if got := BlueServiceName("darwin"); got != "com.kayphoon.tunnelflux.cloudflared-blue" {
		t.Fatalf("darwin blue service=%q", got)
	}
}
