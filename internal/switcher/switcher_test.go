package switcher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kayphoon/cfpick/internal/config"
	"github.com/kayphoon/cfpick/internal/service"
	"github.com/kayphoon/cfpick/internal/slots"
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

func TestReconcileActiveFromServiceUsesOnlyRunningSlot(t *testing.T) {
	st := slots.DefaultState(config.Default())
	st.SetActive(slots.Blue)
	mgr := fakeManager{
		greenStatus: service.SlotStatus{Active: true, PID: 100},
		blueErr:     errors.New("blue inactive"),
	}

	old, changed := reconcileActiveFromService(context.Background(), mgr, &st)
	if !changed || old != slots.Blue || st.Active != slots.Green {
		t.Fatalf("expected stale blue to reconcile to green, old=%q changed=%v state=%+v", old, changed, st)
	}
}

func TestReconcileActiveFromServiceKeepsAmbiguousState(t *testing.T) {
	st := slots.DefaultState(config.Default())
	st.SetActive(slots.Blue)
	mgr := fakeManager{
		greenStatus: service.SlotStatus{Active: true, PID: 100},
		blueStatus:  service.SlotStatus{Active: true, PID: 200},
	}

	old, changed := reconcileActiveFromService(context.Background(), mgr, &st)
	if changed || old != slots.Blue || st.Active != slots.Blue {
		t.Fatalf("expected ambiguous active services to preserve slots state, old=%q changed=%v state=%+v", old, changed, st)
	}
}

type fakeManager struct {
	greenStatus service.SlotStatus
	blueStatus  service.SlotStatus
	greenErr    error
	blueErr     error
}

func (m fakeManager) Name() string { return "fake" }

func (m fakeManager) DiscoverGreen(context.Context, config.Config) (slots.Slot, []string, error) {
	return slots.Slot{}, nil, nil
}

func (m fakeManager) InstallBlue(context.Context, config.Config, slots.Slot, slots.Slot) error {
	return nil
}

func (m fakeManager) Start(context.Context, slots.Slot) error { return nil }

func (m fakeManager) Stop(context.Context, slots.Slot) error { return nil }

func (m fakeManager) Status(_ context.Context, slot slots.Slot) (service.SlotStatus, error) {
	switch slot.Name {
	case slots.Green:
		return m.greenStatus, m.greenErr
	case slots.Blue:
		return m.blueStatus, m.blueErr
	default:
		return service.SlotStatus{}, nil
	}
}

func (m fakeManager) WaitInactive(context.Context, slots.Slot) error { return nil }

func (m fakeManager) WaitDrained(context.Context, int, int, time.Duration) error { return nil }
