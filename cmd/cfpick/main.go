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
	metric := fs.String("metric", "rtt", "rtt, ready, ha, concurrent, request_delta, error_delta, degraded, idle")
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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
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
