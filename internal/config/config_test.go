package config

import "testing"

func TestDefaultSamplingAndRetention(t *testing.T) {
	cfg := Default()
	if cfg.Switching.ProbeIntervalSeconds != 300 {
		t.Fatalf("probe interval=%d, want 300", cfg.Switching.ProbeIntervalSeconds)
	}
	if cfg.Switching.Strategy != SwitchStrategyHot {
		t.Fatalf("strategy=%q, want hot", cfg.Switching.Strategy)
	}
	if cfg.Switching.HotMetricsPortStart != 20241 || cfg.Switching.HotMetricsPortEnd != 20259 {
		t.Fatalf("unexpected hot metrics range: %d-%d", cfg.Switching.HotMetricsPortStart, cfg.Switching.HotMetricsPortEnd)
	}
	if cfg.Runtime.HistoryRetentionDays != 30 {
		t.Fatalf("history retention=%d, want 30", cfg.Runtime.HistoryRetentionDays)
	}
	if cfg.Runtime.SlotsFile == "" {
		t.Fatal("slots file default is empty")
	}
}

func TestWithDefaultsFillsHistoryRetention(t *testing.T) {
	cfg := Config{}
	cfg = cfg.WithDefaults()
	if cfg.Switching.ProbeIntervalSeconds != 300 {
		t.Fatalf("probe interval=%d, want 300", cfg.Switching.ProbeIntervalSeconds)
	}
	if cfg.Runtime.HistoryRetentionDays != 30 {
		t.Fatalf("history retention=%d, want 30", cfg.Runtime.HistoryRetentionDays)
	}
}

func TestWithDefaultsCanDisableHistoryRetention(t *testing.T) {
	cfg := Config{}
	cfg.Runtime.HistoryRetentionDays = -1
	cfg = cfg.WithDefaults()
	if cfg.Runtime.HistoryRetentionDays != -1 {
		t.Fatalf("history retention=%d, want -1", cfg.Runtime.HistoryRetentionDays)
	}
}
