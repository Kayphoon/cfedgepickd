package config

import "testing"

func TestDefaultSamplingAndRetention(t *testing.T) {
	cfg := Default()
	if cfg.Switching.ProbeIntervalSeconds != 300 {
		t.Fatalf("probe interval=%d, want 300", cfg.Switching.ProbeIntervalSeconds)
	}
	if cfg.Runtime.HistoryRetentionDays != 30 {
		t.Fatalf("history retention=%d, want 30", cfg.Runtime.HistoryRetentionDays)
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
