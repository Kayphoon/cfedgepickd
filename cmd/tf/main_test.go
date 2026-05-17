package main

import (
	"strings"
	"testing"
	"time"

	prettytable "github.com/jedib0t/go-pretty/v6/table"
	"github.com/kayphoon/tunnelflux/internal/cloudflared"
	"github.com/kayphoon/tunnelflux/internal/config"
	"github.com/kayphoon/tunnelflux/internal/history"
	"github.com/kayphoon/tunnelflux/internal/slots"
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

func TestParseSwitchIPsDeduplicatesAndNormalizes(t *testing.T) {
	got, err := parseSwitchIPs("198.41.1.1, 198.41.1.2 198.41.1.1")
	if err != nil {
		t.Fatalf("parseSwitchIPs returned error: %v", err)
	}
	if len(got) != 2 || got[0] != "198.41.1.1" || got[1] != "198.41.1.2" {
		t.Fatalf("unexpected IPs: %+v", got)
	}
}

func TestParseSwitchIPsRejectsInvalidIP(t *testing.T) {
	if _, err := parseSwitchIPs("198.41.1.1,not-an-ip"); err == nil {
		t.Fatal("expected invalid IP error")
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

func TestRenderRequestErrorChart(t *testing.T) {
	now := time.Unix(100, 0)
	requests := []history.Point{
		{Time: now, Value: 1},
		{Time: now.Add(time.Minute), Value: 3},
	}
	errors := []history.Point{
		{Time: now, Value: 0},
		{Time: now.Add(time.Minute), Value: 0.2},
	}

	out := renderRequestErrorChart(requests, errors, "24h", 40, 8)
	if !strings.Contains(out, "request_rate and error_rate over 24h") {
		t.Fatalf("chart missing caption:\n%s", out)
	}
	if !strings.Contains(out, "request_rate req/s") || !strings.Contains(out, "error_rate err/s") {
		t.Fatalf("chart missing legends:\n%s", out)
	}
}

func TestRenderEdgeComparison(t *testing.T) {
	r := history.Record{
		TopProbeResults: []history.IPProbe{
			{IP: "198.41.1.1", OK: 8, Fail: 0, MedianMS: 4, Score: 4.5},
			{IP: "198.41.1.2", OK: 8, Fail: 0, MedianMS: 5, Score: 5.5},
		},
		CurrentProbeResults: []history.IPProbe{
			{IP: "198.41.2.1", OK: 8, Fail: 0, MedianMS: 20, Score: 20.5},
		},
	}

	out := renderEdgeComparison(r, 3)
	if !strings.Contains(out, "Edge Comparison") || !strings.Contains(out, "CURRENT #1") {
		t.Fatalf("comparison table missing expected rows:\n%s", out)
	}
	if !strings.Contains(out, "SLOW") || !strings.Contains(out, "+16.00 ms / 5.0x") {
		t.Fatalf("comparison table missing slow delta:\n%s", out)
	}
}

func TestRenderEdgeComparisonFallback(t *testing.T) {
	r := history.Record{
		TopIP:       "198.41.1.1",
		TopMedianMS: 4,
		CurrentIPs:  []string{"198.41.2.1"},
	}

	out := renderEdgeComparison(r, 3)
	if !strings.Contains(out, "198.41.1.1") || !strings.Contains(out, "198.41.2.1") {
		t.Fatalf("comparison table missing fallback IPs:\n%s", out)
	}
}

func TestIsRequestRateMetric(t *testing.T) {
	for _, metric := range []string{"", "request_rate", "requests_rate", "rps"} {
		if !isRequestRateMetric(metric) {
			t.Fatalf("%q should be request-rate chart metric", metric)
		}
	}
	if isRequestRateMetric("error_rate") {
		t.Fatal("error_rate should not select the default combined chart")
	}
}

func TestRenderOverviewUsesTable(t *testing.T) {
	out := renderKVTable("Test", []prettytable.Row{{"A", "B"}})
	if !strings.Contains(out, "Test") || !strings.Contains(out, "FIELD") {
		t.Fatalf("unexpected table:\n%s", out)
	}
}

func TestRenderSlotsUsesResolvedActiveEndpoint(t *testing.T) {
	st := slots.DefaultState(config.Default())
	st.SetActive(slots.Blue)
	endpoint := slots.ActiveEndpoint{
		Slot:       st.Green,
		State:      st,
		MetricsURL: st.Green.MetricsURL,
		ReadyURL:   st.Green.ReadyURL,
		Source:     "slots.green",
	}

	out := renderSlots(endpoint)
	if !strings.Contains(out, "Active") || !strings.Contains(out, "green") || !strings.Contains(out, "slots.green") {
		t.Fatalf("active row missing resolved green endpoint:\n%s", out)
	}
	if !strings.Contains(out, "Green  | ACTIVE") {
		t.Fatalf("green row should use resolved endpoint state, not stale slots.active:\n%s", out)
	}
	if !strings.Contains(out, "Blue   | STANDBY") {
		t.Fatalf("blue row should not remain active when fallback resolved green:\n%s", out)
	}
}

func TestRenderStatusSummaryChinese(t *testing.T) {
	cfg := config.Default()
	cfg.Switching.EmergencyRTTThresholdMS = 100
	endpoint := slots.ActiveEndpoint{
		Slot:       slots.DefaultState(cfg).Green,
		State:      slots.DefaultState(cfg),
		MetricsURL: cfg.Cloudflared.MetricsURL,
		ReadyURL:   cfg.Cloudflared.ReadyURL,
		Source:     "slots.active",
	}
	latest := history.Record{
		Time:              time.Unix(200, 0),
		EffectiveProtocol: config.ProtocolQUIC,
		TopIP:             "198.41.1.1",
		TopMedianMS:       4,
		ReadyConnections:  2,
		HAConnections:     2,
		TotalRequests:     100,
	}

	out := renderStatusSummary(
		"/etc/tunnelflux/config.json",
		cfg,
		"/var/lib/tunnelflux/history.jsonl",
		"request_rate",
		"24h",
		endpoint,
		cloudflared.Ready{ReadyConnections: 2, ConnectorID: "abc"},
		nil,
		cloudflared.Metrics{HAConnections: 2, TotalRequests: 100},
		nil,
		nil,
		&latest,
		"zh",
	)
	for _, want := range []string{"状态总览", "配置", "应急 RTT", "当前槽位", "最近采样"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestNormalizeStatusLang(t *testing.T) {
	if got := normalizeStatusLang("en", true); got != "zh" {
		t.Fatalf("zh flag lang=%q", got)
	}
	if got := normalizeStatusLang("zh-CN", false); got != "zh" {
		t.Fatalf("zh-CN lang=%q", got)
	}
	if got := normalizeStatusLang("unknown", false); got != "en" {
		t.Fatalf("fallback lang=%q", got)
	}
}

func TestPerformanceFromHistory(t *testing.T) {
	now := time.Unix(200, 0)
	records := []history.Record{
		{
			Time:                  now.Add(-10 * time.Second),
			TotalRequests:         100,
			RequestErrors:         1,
			ResponseByCode:        map[string]float64{"500": 3},
			Response5xx:           3,
			ProcessCPUSeconds:     10,
			ProcessNetworkRxBytes: 1000,
			ProcessNetworkTxBytes: 2000,
		},
		{
			Time:                  now,
			TotalRequests:         120,
			RequestErrors:         2,
			ResponseByCode:        map[string]float64{"500": 4},
			Response5xx:           4,
			ProcessCPUSeconds:     11,
			ProcessNetworkRxBytes: 2000,
			ProcessNetworkTxBytes: 5000,
		},
	}

	got := performanceFromHistory(records)
	if !got.HasLast || got.RequestRate != 2 || got.ErrorRate != 0.1 || got.Response5xxDelta != 1 {
		t.Fatalf("unexpected performance delta: %+v", got)
	}
	if got.CPUPercent != 10 || got.NetworkRxRate != 100 || got.NetworkTxRate != 300 {
		t.Fatalf("unexpected runtime delta: %+v", got)
	}
}
