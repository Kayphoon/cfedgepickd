package switcher

import (
	"context"
	"strings"
	"testing"

	"github.com/kayphoon/cfpick/internal/config"
)

func TestApplyUsesRestartStrategyWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Switching.Strategy = config.SwitchStrategyRestart
	cfg.Runtime.DryRun = true
	res, err := Apply(context.Background(), cfg, []string{"198.41.1.1", "198.41.1.2"}, config.ProtocolAuto)
	if err != nil {
		t.Fatal(err)
	}
	if res.Strategy != config.SwitchStrategyRestart || !strings.Contains(res.Message, "restart") {
		t.Fatalf("unexpected restart result: %+v", res)
	}
}

func TestApplyHotDryRunPlansSlotSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.DryRun = true
	cfg.Switching.Strategy = config.SwitchStrategyHot
	cfg.Cloudflared.Systemctl = "systemctl-does-not-exist"
	res, err := Apply(context.Background(), cfg, []string{"198.41.1.1", "198.41.1.2"}, config.ProtocolAuto)
	if err != nil {
		t.Fatal(err)
	}
	if res.Strategy != config.SwitchStrategyHot || res.ActiveSlot != "green" || res.InactiveSlot != "blue" {
		t.Fatalf("unexpected hot dry-run result: %+v", res)
	}
	if !strings.Contains(res.Message, "gracefully stop") {
		t.Fatalf("unexpected dry-run message: %s", res.Message)
	}
}
