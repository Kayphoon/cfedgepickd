package main

import (
	"strings"
	"testing"
	"time"

	prettytable "github.com/jedib0t/go-pretty/v6/table"
	"github.com/kayphoon/cfpick/internal/cloudflared"
	"github.com/kayphoon/cfpick/internal/history"
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

func TestRenderLineChart(t *testing.T) {
	now := time.Unix(100, 0)
	points := []history.Point{
		{Time: now, Value: 3.5},
		{Time: now.Add(time.Minute), Value: 4.2},
	}
	sum := history.Summary{
		Metric: "rtt",
		Count:  2,
		Min:    3.5,
		Max:    4.2,
		Avg:    3.85,
		Latest: 4.2,
		From:   points[0].Time,
		To:     points[1].Time,
	}

	out := renderLineChart(points, sum, "24h", 40, 8)
	if !strings.Contains(out, "rtt over 24h") {
		t.Fatalf("chart missing caption:\n%s", out)
	}
}

func TestRenderOverviewUsesTable(t *testing.T) {
	out := renderKVTable("Test", []prettytable.Row{{"A", "B"}})
	if !strings.Contains(out, "Test") || !strings.Contains(out, "FIELD") {
		t.Fatalf("unexpected table:\n%s", out)
	}
}

func TestPerformanceSinceLast(t *testing.T) {
	now := time.Unix(200, 0)
	records := []history.Record{{
		Time:                  now.Add(-10 * time.Second),
		TotalRequests:         100,
		RequestErrors:         1,
		Response5xx:           3,
		ProcessCPUSeconds:     10,
		ProcessNetworkRxBytes: 1000,
		ProcessNetworkTxBytes: 2000,
	}}
	metrics := cloudflared.Metrics{
		TotalRequests:         120,
		RequestErrors:         2,
		Response5xx:           4,
		ProcessCPUSeconds:     11,
		ProcessNetworkRxBytes: 2000,
		ProcessNetworkTxBytes: 5000,
	}

	got := performanceSinceLast(metrics, records, now)
	if !got.HasLast || got.RequestRate != 2 || got.ErrorRate != 0.1 || got.Response5xxDelta != 1 {
		t.Fatalf("unexpected performance delta: %+v", got)
	}
	if got.CPUPercent != 10 || got.NetworkRxRate != 100 || got.NetworkTxRate != 300 {
		t.Fatalf("unexpected runtime delta: %+v", got)
	}
}
