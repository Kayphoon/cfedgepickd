package main

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
	}

	for _, tt := range tests {
		got, err := parseWindow(tt.raw)
		if err != nil {
			t.Fatalf("parseWindow(%q) returned error: %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("parseWindow(%q)=%s, want %s", tt.raw, got, tt.want)
		}
	}
}

func TestParseWindowRejectsBadDays(t *testing.T) {
	if _, err := parseWindow("xd"); err == nil {
		t.Fatal("expected invalid day window error")
	}
}
