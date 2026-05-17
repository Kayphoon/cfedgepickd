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
	if cfg.Runtime.Language != LanguageEN {
		t.Fatalf("runtime language=%q, want en", cfg.Runtime.Language)
	}
	if cfg.Runtime.SlotsFile == "" {
		t.Fatal("slots file default is empty")
	}
	if cfg.Switching.EmergencyRTTThresholdMS != 0 {
		t.Fatalf("emergency rtt threshold=%f, want disabled", cfg.Switching.EmergencyRTTThresholdMS)
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
	if cfg.Runtime.Language != LanguageEN {
		t.Fatalf("runtime language=%q, want en", cfg.Runtime.Language)
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

func TestValidateRejectsNegativeEmergencyRTTThreshold(t *testing.T) {
	cfg := Default()
	cfg.Switching.EmergencyRTTThresholdMS = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative emergency RTT threshold to be invalid")
	}
}

func TestLanguageParsingAndValidation(t *testing.T) {
	if got := NormalizeLanguage("zh-CN"); got != LanguageZH {
		t.Fatalf("NormalizeLanguage zh-CN=%q, want zh", got)
	}
	cfg := Default()
	cfg.Runtime.Language = "zh-CN"
	cfg = cfg.WithDefaults()
	if cfg.Runtime.Language != LanguageZH {
		t.Fatalf("WithDefaults language=%q, want zh", cfg.Runtime.Language)
	}
	cfg.Runtime.Language = "bad"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid runtime.language to be rejected")
	}
}
