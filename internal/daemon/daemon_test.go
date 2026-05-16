package daemon

import (
	"testing"
	"time"

	"github.com/kayphoon/cfedgepickd/internal/cloudflared"
	"github.com/kayphoon/cfedgepickd/internal/config"
	"github.com/kayphoon/cfedgepickd/internal/probe"
	"github.com/kayphoon/cfedgepickd/internal/state"
)

func TestIdleStateStartsWindowThenBecomesIdle(t *testing.T) {
	cfg := config.Default()
	cfg.Switching.IdleWindowSeconds = 10
	now := time.Unix(100, 0)
	st := state.State{
		LastProbeAt:       now.Add(-time.Minute),
		LastTotalRequests: 42,
	}
	metrics := cloudflared.Metrics{TotalRequests: 42}

	idle, since := idleState(st, metrics, cfg, now)
	if idle {
		t.Fatal("first unchanged cycle should start the idle window, not pass it")
	}
	if !since.Equal(now) {
		t.Fatalf("idle window should start now, got %s", since)
	}

	st.IdleSince = since
	idle, _ = idleState(st, metrics, cfg, now.Add(11*time.Second))
	if !idle {
		t.Fatal("unchanged traffic after idle window should be idle")
	}
}

func TestProtocolForCloudflaredConfigKeepsAuto(t *testing.T) {
	cfg := config.Default()
	cfg.Cloudflared.Protocol = config.ProtocolAuto
	pr := probe.Report{EffectiveProtocol: config.ProtocolQUIC}

	if got := protocolForCloudflaredConfig(cfg, pr); got != config.ProtocolAuto {
		t.Fatalf("protocol = %q, want auto", got)
	}
}

func TestProtocolForCloudflaredConfigKeepsExplicitProtocol(t *testing.T) {
	cfg := config.Default()
	cfg.Cloudflared.Protocol = config.ProtocolQUIC
	pr := probe.Report{EffectiveProtocol: config.ProtocolHTTP2}

	if got := protocolForCloudflaredConfig(cfg, pr); got != config.ProtocolQUIC {
		t.Fatalf("protocol = %q, want quic", got)
	}
}
