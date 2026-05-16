package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/guptarohit/asciigraph"
	prettytable "github.com/jedib0t/go-pretty/v6/table"
	"github.com/kayphoon/cfpick/internal/cloudflared"
	"github.com/kayphoon/cfpick/internal/config"
	"github.com/kayphoon/cfpick/internal/daemon"
	"github.com/kayphoon/cfpick/internal/discover"
	"github.com/kayphoon/cfpick/internal/history"
	"github.com/kayphoon/cfpick/internal/install"
	"github.com/kayphoon/cfpick/internal/probe"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "status":
		statusCmd(os.Args[2:])
	case "run":
		runCmd(os.Args[2:])
	case "once":
		onceCmd(os.Args[2:])
	case "discover":
		discoverCmd(os.Args[2:])
	case "probe":
		probeCmd(os.Args[2:])
	case "install":
		installCmd(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func statusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := fs.String("config", "", "config file")
	metric := fs.String("metric", "request_rate", "request_rate, error_rate, response_5xx_delta, rss_mb, goroutines, cpu_percent, network_rx_rate, rtt")
	since := fs.String("since", "24h", "history window, for example 24h or 7d")
	width := fs.Int("width", 80, "graph width")
	height := fs.Int("height", 12, "graph height")
	_ = fs.Parse(args)

	path := resolveConfigPath(*configPath)
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	window, err := parseWindow(*since)
	if err != nil {
		log.Fatalf("invalid --since: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	historyPath := resolveHistoryPath(cfg.Runtime.HistoryFile)

	ready, readyErr := cloudflared.FetchReady(ctx, cfg.Cloudflared.ReadyURL)
	metrics, metricsErr := cloudflared.FetchMetrics(ctx, cfg.Cloudflared.MetricsURL)
	conns, edgesErr := cloudflared.CurrentEdges(ctx, cfg.Edge.Port)

	records, err := history.ReadSince(historyPath, time.Now().Add(-window))
	if err != nil {
		log.Fatalf("read history: %v", err)
	}

	fmt.Printf("cfpick status  %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(renderOverview(path, cfg, historyPath, *metric, *since))
	fmt.Println()
	fmt.Println(renderHealth(ready, readyErr, metrics, metricsErr))
	fmt.Println()
	fmt.Println(renderPerformance(metrics, metricsErr, records, time.Now()))
	fmt.Println()
	fmt.Println(renderEdges(conns, edgesErr))

	if len(records) == 0 {
		fmt.Println()
		fmt.Printf("History: no records in last %s at %s\n", *since, historyPath)
		return
	}

	latest := records[len(records)-1]
	fmt.Println()
	fmt.Println(renderLatest(latest))

	points, summary, err := history.Series(records, *metric)
	if err != nil {
		log.Fatalf("series: %v", err)
	}
	fmt.Println()
	fmt.Println(renderTrendSummary(summary, *since))
	if len(points) == 0 {
		fmt.Printf("Trend: no points for metric %q in last %s\n", *metric, *since)
		return
	}
	fmt.Println()
	fmt.Println(renderLineChart(points, summary, *since, *width, *height))
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", resolveConfigPath(""), "config file")
	dryRun := fs.Bool("dry-run", false, "force dry-run")
	apply := fs.Bool("apply", false, "force apply mode")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *dryRun {
		cfg.Runtime.DryRun = true
	}
	if *apply {
		cfg.Runtime.DryRun = false
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	if err := daemon.Run(context.Background(), cfg); err != nil {
		log.Fatalf("daemon stopped: %v", err)
	}
}

func onceCmd(args []string) {
	fs := flag.NewFlagSet("once", flag.ExitOnError)
	configPath := fs.String("config", resolveConfigPath(""), "config file")
	apply := fs.Bool("apply", false, "apply if eligible")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if !*apply {
		cfg.Runtime.DryRun = true
	}
	decision, sw, err := daemon.Once(context.Background(), cfg, *apply)
	if err != nil {
		log.Fatalf("once failed: %v", err)
	}
	daemon.PrintDecision(decision, sw)
}

func discoverCmd(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	pretty := fs.Bool("pretty", true, "pretty JSON")
	_ = fs.Parse(args)
	rep, err := discover.Run(context.Background())
	if err != nil {
		log.Fatalf("discover failed: %v", err)
	}
	printJSON(rep, *pretty)
}

func probeCmd(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("config", resolveConfigPath(""), "config file")
	mode := fs.String("protocol", "", "auto, quic, or http2")
	top := fs.Int("top", 0, "override top_n")
	pretty := fs.Bool("pretty", true, "pretty JSON")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *top > 0 {
		cfg.Edge.TopN = *top
	}
	if *mode != "" {
		cfg.Cloudflared.Protocol = *mode
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	rep, err := probe.Run(context.Background(), cfg, probe.Mode(cfg.Cloudflared.Protocol))
	if err != nil {
		printJSON(rep, *pretty)
		log.Fatalf("probe failed: %v", err)
	}
	printJSON(rep, *pretty)
}

func installCmd(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	apply := fs.Bool("apply", false, "write config and systemd unit")
	dryRun := fs.Bool("dry-run", false, "print discovered config and planned writes without changing files")
	protocol := fs.String("protocol", "auto", "auto, quic, or http2")
	configPath := fs.String("config", config.DefaultConfigPath, "target config path")
	binary := fs.String("binary", config.DefaultBinaryPath, "binary path in systemd unit")
	unit := fs.String("unit", config.DefaultUnitPath, "target systemd unit path")
	pretty := fs.Bool("pretty", true, "pretty JSON")
	_ = fs.Parse(args)
	applyMode := *apply && !*dryRun
	rep, err := install.Run(context.Background(), install.Options{
		Apply:    applyMode,
		Protocol: *protocol,
		Config:   *configPath,
		Binary:   *binary,
		UnitPath: *unit,
	})
	if err != nil {
		log.Printf("install completed with probe warning/error: %v", err)
	}
	printJSON(rep, *pretty)
	if err != nil && applyMode {
		os.Exit(1)
	}
}

func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat(config.DefaultConfigPath); err == nil {
		return config.DefaultConfigPath
	}
	if _, err := os.Stat(config.LegacyConfigPath); err == nil {
		return config.LegacyConfigPath
	}
	return config.DefaultConfigPath
}

func resolveHistoryPath(path string) string {
	if path == "" {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	const legacy = "/var/lib/cfedgepickd/history.jsonl"
	if path == "/var/lib/cfpick/history.jsonl" {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return path
}

func edgeRemotes(conns []cloudflared.EdgeConnection) []string {
	out := make([]string, 0, len(conns))
	seen := map[string]bool{}
	for _, c := range conns {
		if c.Remote == "" || seen[c.Remote] {
			continue
		}
		seen[c.Remote] = true
		out = append(out, c.Remote)
	}
	return out
}

func renderOverview(configPath string, cfg config.Config, historyPath, metric, since string) string {
	return renderKVTable("Overview", []prettytable.Row{
		{"Config", configPath},
		{"Protocol", cfg.Cloudflared.Protocol},
		{"Metrics", cfg.Cloudflared.MetricsURL},
		{"Ready", cfg.Cloudflared.ReadyURL},
		{"History", historyPath},
		{"Window", since},
		{"Metric", metric},
	})
}

func renderHealth(ready cloudflared.Ready, readyErr error, metrics cloudflared.Metrics, metricsErr error) string {
	rows := []prettytable.Row{}
	if readyErr != nil {
		rows = append(rows, prettytable.Row{"Ready", "UNKNOWN", readyErr.Error()})
	} else {
		rows = append(rows, prettytable.Row{
			"Ready",
			statusLabel(ready.ReadyConnections >= 2),
			fmt.Sprintf("connections=%d connector=%s", ready.ReadyConnections, emptyDash(ready.ConnectorID)),
		})
	}
	if metricsErr != nil {
		rows = append(rows, prettytable.Row{"Metrics", "UNKNOWN", metricsErr.Error()})
	} else {
		rows = append(rows,
			prettytable.Row{"HA", statusLabel(metrics.HAConnections >= 2), fmt.Sprintf("ha_connections=%d", metrics.HAConnections)},
			prettytable.Row{"Traffic", trafficLabel(metrics.ConcurrentRequests), fmt.Sprintf("concurrent=%d total_requests=%.0f", metrics.ConcurrentRequests, metrics.TotalRequests)},
			prettytable.Row{"Errors", errorLabel(metrics.RequestErrors), fmt.Sprintf("request_errors=%.0f", metrics.RequestErrors)},
		)
	}
	return renderTable("Health", []interface{}{"Signal", "State", "Detail"}, rows)
}

func renderPerformance(metrics cloudflared.Metrics, metricsErr error, records []history.Record, now time.Time) string {
	if metricsErr != nil {
		return renderTable("Performance", []interface{}{"Signal", "State", "Detail"}, []prettytable.Row{
			{"Metrics", "UNKNOWN", metricsErr.Error()},
		})
	}
	delta := performanceSinceLast(metrics, records, now)
	rows := []prettytable.Row{
		{
			"Requests",
			activityLabel(delta.RequestRate),
			fmt.Sprintf("%s, +%.0f since sample, total=%.0f", formatPerSecond(delta.RequestRate, "req"), delta.RequestDelta, metrics.TotalRequests),
		},
		{
			"Errors",
			statusLabel(delta.ErrorDelta == 0),
			fmt.Sprintf("%s, +%.0f since sample, total=%.0f", formatPerSecond(delta.ErrorRate, "err"), delta.ErrorDelta, metrics.RequestErrors),
		},
		{
			"HTTP Codes",
			statusLabel(delta.Response5xxDelta == 0),
			fmt.Sprintf("2xx=%.0f 3xx=%.0f 4xx=%.0f 5xx=%.0f, delta_5xx=%.0f", metrics.Response2xx, metrics.Response3xx, metrics.Response4xx, metrics.Response5xx, delta.Response5xxDelta),
		},
		{
			"Connect Latency",
			availabilityLabel(metrics.ProxyConnectLatencyHits > 0),
			fmt.Sprintf("avg=%s samples=%.0f", formatMS(delta.ProxyConnectAvgMS), metrics.ProxyConnectLatencyHits),
		},
		{
			"Sessions",
			activityLabel(metrics.TCPActiveSessions + metrics.UDPActiveSessions),
			fmt.Sprintf("tcp_active=%.0f tcp_total=%.0f udp_active=%.0f udp_total=%.0f", metrics.TCPActiveSessions, metrics.TCPTotalSessions, metrics.UDPActiveSessions, metrics.UDPTotalSessions),
		},
		{
			"Runtime",
			"OK",
			fmt.Sprintf("rss=%s heap=%s goroutines=%.0f threads=%.0f cpu=%s", formatBytes(metrics.ProcessRSSBytes), formatBytes(metrics.GoHeapAllocBytes), metrics.GoGoroutines, metrics.GoThreads, formatPercent(delta.CPUPercent)),
		},
		{
			"Network",
			activityLabel(delta.NetworkRxRate + delta.NetworkTxRate),
			fmt.Sprintf("rx=%s (%s) tx=%s (%s)", formatBytes(metrics.ProcessNetworkRxBytes), formatBytesPerSecond(delta.NetworkRxRate), formatBytes(metrics.ProcessNetworkTxBytes), formatBytesPerSecond(delta.NetworkTxRate)),
		},
	}
	if delta.HasLast {
		rows = append(rows, prettytable.Row{"Sample Gap", "INFO", delta.Elapsed.Round(time.Second).String()})
	} else {
		rows = append(rows, prettytable.Row{"Sample Gap", "INFO", "no previous history sample"})
	}
	return renderTable("Performance", []interface{}{"Signal", "State", "Detail"}, rows)
}

func renderEdges(conns []cloudflared.EdgeConnection, err error) string {
	if err != nil {
		return renderTable("Current Edges", []interface{}{"State", "Detail"}, []prettytable.Row{
			{"UNKNOWN", err.Error()},
		})
	}
	if len(conns) == 0 {
		return renderTable("Current Edges", []interface{}{"State", "Detail"}, []prettytable.Row{
			{"EMPTY", "no cloudflared :7844 sockets found"},
		})
	}
	rows := make([]prettytable.Row, 0, len(conns))
	for i, conn := range conns {
		rows = append(rows, prettytable.Row{i + 1, conn.IP, conn.Remote, conn.Local})
	}
	return renderTable("Current Edges", []interface{}{"#", "IP", "Remote", "Local"}, rows)
}

func renderLatest(r history.Record) string {
	return renderKVTable("Latest Sample", []prettytable.Row{
		{"Time", formatTime(r.Time)},
		{"Effective Protocol", emptyDash(r.EffectiveProtocol)},
		{"Top IP", emptyDash(r.TopIP)},
		{"Top RTT", formatMS(r.TopMedianMS)},
		{"Ready / HA", fmt.Sprintf("%d / %d", r.ReadyConnections, r.HAConnections)},
		{"Requests / Errors", fmt.Sprintf("%.0f / %.0f", r.TotalRequests, r.RequestErrors)},
		{"HTTP 2xx/3xx/4xx/5xx", fmt.Sprintf("%.0f / %.0f / %.0f / %.0f", r.Response2xx, r.Response3xx, r.Response4xx, r.Response5xx)},
		{"RSS / Goroutines", fmt.Sprintf("%s / %.0f", formatBytes(r.ProcessRSSBytes), r.GoGoroutines)},
		{"Idle", yesNo(r.Idle)},
		{"Degraded", yesNo(r.Degraded)},
		{"Should Switch", yesNo(r.ShouldSwitch)},
		{"Switch Applied", yesNo(r.SwitchApplied)},
		{"Reason", emptyDash(r.Reason)},
	})
}

func renderTrendSummary(sum history.Summary, since string) string {
	if sum.Count == 0 {
		return renderTable("Trend", []interface{}{"Window", "Metric", "Points"}, []prettytable.Row{
			{since, sum.Metric, 0},
		})
	}
	return renderTable("Trend", []interface{}{"Window", "Metric", "Points", "Latest", "Min", "Avg", "Max", "From", "To"}, []prettytable.Row{
		{
			since,
			sum.Metric,
			sum.Count,
			fmt.Sprintf("%.2f", sum.Latest),
			fmt.Sprintf("%.2f", sum.Min),
			fmt.Sprintf("%.2f", sum.Avg),
			fmt.Sprintf("%.2f", sum.Max),
			formatTime(sum.From),
			formatTime(sum.To),
		},
	})
}

func renderLineChart(points []history.Point, sum history.Summary, since string, width, height int) string {
	values := make([]float64, 0, len(points))
	for _, point := range points {
		values = append(values, point.Value)
	}
	width = clamp(width, 20, 160)
	height = clamp(height, 4, 40)
	opts := []asciigraph.Option{
		asciigraph.Width(width),
		asciigraph.Height(height),
		asciigraph.Precision(2),
		asciigraph.Caption(fmt.Sprintf("%s over %s", sum.Metric, since)),
	}
	if sum.Min == sum.Max {
		opts = append(opts, asciigraph.LowerBound(sum.Min-1), asciigraph.UpperBound(sum.Max+1))
	}
	return asciigraph.Plot(values, opts...)
}

func renderKVTable(title string, rows []prettytable.Row) string {
	return renderTable(title, []interface{}{"Field", "Value"}, rows)
}

func renderTable(title string, header []interface{}, rows []prettytable.Row) string {
	t := prettytable.NewWriter()
	t.SetTitle(title)
	t.SetStyle(prettytable.StyleDefault)
	t.AppendHeader(prettytable.Row(header))
	t.AppendRows(rows)
	return t.Render()
}

func statusLabel(ok bool) string {
	if ok {
		return "OK"
	}
	return "WARN"
}

func trafficLabel(concurrent int) string {
	if concurrent == 0 {
		return "IDLE"
	}
	return "ACTIVE"
}

func activityLabel(v float64) string {
	if v == 0 {
		return "IDLE"
	}
	return "ACTIVE"
}

func availabilityLabel(v bool) string {
	if v {
		return "OK"
	}
	return "N/A"
}

func errorLabel(errors float64) string {
	if errors > 0 {
		return "SEEN"
	}
	return "OK"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func emptyDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func formatMS(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f ms", v)
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.2f%%", v)
}

func formatPerSecond(v float64, unit string) string {
	return fmt.Sprintf("%.2f %s/s", v, unit)
}

func formatBytesPerSecond(v float64) string {
	return formatBytes(v) + "/s"
}

func formatBytes(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	for _, unit := range units {
		if v < 1024 || unit == units[len(units)-1] {
			if unit == "B" {
				return fmt.Sprintf("%.0f %s", v, unit)
			}
			return fmt.Sprintf("%.2f %s", v, unit)
		}
		v /= 1024
	}
	return "0 B"
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

type performanceDelta struct {
	HasLast           bool
	Elapsed           time.Duration
	RequestDelta      float64
	RequestRate       float64
	ErrorDelta        float64
	ErrorRate         float64
	Response5xxDelta  float64
	ProxyConnectAvgMS float64
	CPUPercent        float64
	NetworkRxRate     float64
	NetworkTxRate     float64
}

func performanceSinceLast(metrics cloudflared.Metrics, records []history.Record, now time.Time) performanceDelta {
	d := performanceDelta{}
	if metrics.ProxyConnectLatencyHits > 0 {
		d.ProxyConnectAvgMS = metrics.ProxyConnectLatencySum / metrics.ProxyConnectLatencyHits
	}
	if len(records) == 0 {
		return d
	}
	last := records[len(records)-1]
	elapsed := now.Sub(last.Time)
	if elapsed <= 0 {
		return d
	}
	seconds := elapsed.Seconds()
	d.HasLast = true
	d.Elapsed = elapsed
	d.RequestDelta = nonNegativeDelta(metrics.TotalRequests, last.TotalRequests)
	d.RequestRate = d.RequestDelta / seconds
	d.ErrorDelta = nonNegativeDelta(metrics.RequestErrors, last.RequestErrors)
	d.ErrorRate = d.ErrorDelta / seconds
	if last.ResponseByCode != nil || last.Response2xx != 0 || last.Response3xx != 0 || last.Response4xx != 0 || last.Response5xx != 0 {
		d.Response5xxDelta = nonNegativeDelta(metrics.Response5xx, last.Response5xx)
	}
	if metrics.ProxyConnectLatencyHits > last.ProxyConnectLatencyHits {
		d.ProxyConnectAvgMS = nonNegativeDelta(metrics.ProxyConnectLatencySum, last.ProxyConnectLatencySum) / nonNegativeDelta(metrics.ProxyConnectLatencyHits, last.ProxyConnectLatencyHits)
	}
	if last.ProcessCPUSeconds > 0 {
		d.CPUPercent = 100 * nonNegativeDelta(metrics.ProcessCPUSeconds, last.ProcessCPUSeconds) / seconds
	}
	if last.ProcessNetworkRxBytes > 0 {
		d.NetworkRxRate = nonNegativeDelta(metrics.ProcessNetworkRxBytes, last.ProcessNetworkRxBytes) / seconds
	}
	if last.ProcessNetworkTxBytes > 0 {
		d.NetworkTxRate = nonNegativeDelta(metrics.ProcessNetworkTxBytes, last.ProcessNetworkTxBytes) / seconds
	}
	return d
}

func nonNegativeDelta(now, before float64) float64 {
	if now < before {
		return 0
	}
	return now - before
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func printJSON(v any, pretty bool) {
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		log.Fatalf("json: %v", err)
	}
	fmt.Println(string(data))
}

func parseWindow(raw string) (time.Duration, error) {
	if raw == "" {
		return 24 * time.Hour, nil
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, err
		}
		if days <= 0 {
			return 0, errors.New("day window must be positive")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("window must be positive")
	}
	return d, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: cfpick status|run|once|discover|probe|install|version [flags]\n")
}
