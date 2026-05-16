package main

import (
	"testing"
	"time"

	"github.com/kayphoon/cfedgepickd/internal/cloudflared"
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

func TestParseWindowRejectsNonPositive(t *testing.T) {
	if _, err := parseWindow("0h"); err == nil {
		t.Fatal("expected zero duration error")
	}
	if _, err := parseWindow("0d"); err == nil {
		t.Fatal("expected zero day window error")
	}
}

func TestEdgeRemotesDeduplicates(t *testing.T) {
	got := edgeRemotes([]cloudflared.EdgeConnection{
		{Remote: "198.41.1.1:7844"},
		{Remote: "198.41.1.1:7844"},
		{Remote: "198.41.1.2:7844"},
	})
	if len(got) != 2 || got[0] != "198.41.1.1:7844" || got[1] != "198.41.1.2:7844" {
		t.Fatalf("unexpected remotes: %+v", got)
	}
}
