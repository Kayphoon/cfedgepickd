package daemon

import (
	"testing"
	"time"

	"github.com/kayphoon/cfpick/internal/cloudflared"
	"github.com/kayphoon/cfpick/internal/config"
	"github.com/kayphoon/cfpick/internal/probe"
	"github.com/kayphoon/cfpick/internal/state"
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

func TestCurrentProbeSamplesPreserveCurrentOrder(t *testing.T) {
	got := currentProbeSamples([]string{"198.41.2.2", "198.41.2.1", "198.41.2.2", "198.41.2.3"}, []probe.Result{
		{IP: "198.41.2.1", OK: 8, Fail: 0, MedianMS: 12},
		{IP: "198.41.2.2", OK: 7, Fail: 1, MedianMS: 18},
	})

	if len(got) != 3 {
		t.Fatalf("samples=%d, want 3", len(got))
	}
	if got[0].IP != "198.41.2.2" || got[0].MedianMS != 18 {
		t.Fatalf("first sample=%+v", got[0])
	}
	if got[1].IP != "198.41.2.1" || got[1].MedianMS != 12 {
		t.Fatalf("second sample=%+v", got[1])
	}
	if got[2].IP != "198.41.2.3" || got[2].MedianMS != 0 {
		t.Fatalf("missing probe sample=%+v", got[2])
	}
}
