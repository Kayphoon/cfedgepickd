package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/guptarohit/asciigraph"
	prettytable "github.com/jedib0t/go-pretty/v6/table"

	"github.com/Kayphoon/TunnelFlux/internal/cli"
	"github.com/Kayphoon/TunnelFlux/internal/cloudflared"
	"github.com/Kayphoon/TunnelFlux/internal/config"
	"github.com/Kayphoon/TunnelFlux/internal/daemon"
	"github.com/Kayphoon/TunnelFlux/internal/history"
	"github.com/Kayphoon/TunnelFlux/internal/hosts"
	"github.com/Kayphoon/TunnelFlux/internal/slots"
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
	case "switch":
		switchCmd(os.Args[2:])
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
	langFlag := fs.String("lang", "en", "status language: en or zh")
	zhFlag := fs.Bool("zh", false, "shortcut for --lang zh")
	_ = fs.Parse(args)
	lang := normalizeStatusLang(*langFlag, *zhFlag)

	path := resolveConfigPath(*configPath)
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	window, err := cli.ParseWindow(*since)
	if err != nil {
		log.Fatalf("invalid --since: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	endpoint := slots.ResolveActiveEndpoint(ctx, cfg)
	if endpoint.MetricsURL != "" {
		cfg.Cloudflared.MetricsURL = endpoint.MetricsURL
	}
	if endpoint.ReadyURL != "" {
		cfg.Cloudflared.ReadyURL = endpoint.ReadyURL
	}
	historyPath := resolveHistoryPath(cfg.Runtime.HistoryFile)

	ready, readyErr := cloudflared.FetchReady(ctx, cfg.Cloudflared.ReadyURL)
	metrics, metricsErr := cloudflared.FetchMetrics(ctx, cfg.Cloudflared.MetricsURL)
	conns, edgesErr := cloudflared.CurrentEdges(ctx, cfg.Edge.Port)
	if edgesErr == nil && len(conns) == 0 {
		if current, err := hosts.Current(cfg.Runtime.HostsFile, cfg.Edge.Hostnames); err == nil {
			conns = hostMappingEdges(current, cfg.Edge.Hostnames)
		}
	}

	records, err := history.ReadSince(historyPath, time.Now().Add(-window))
	if err != nil {
		log.Fatalf("read history: %v", err)
	}

	var latest *history.Record
	if len(records) > 0 {
		latest = &records[len(records)-1]
	}

	fmt.Printf("tunnelflux status  %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(renderStatusSummary(path, cfg, historyPath, *metric, *since, endpoint, ready, readyErr, metrics, metricsErr, records, latest, lang))
	fmt.Println()
	fmt.Println(renderEdgesLocalized(conns, edgesErr, lang))

	if len(records) == 0 {
		fmt.Println()
		fmt.Printf("%s: %s %s %s\n", tr(lang, "History"), tr(lang, "no records in last"), *since, historyPath)
		return
	}

	fmt.Println()
	fmt.Println(renderEdgeComparisonLocalized(*latest, cfg.Switching.DegradedFactor, lang))

	if isRequestRateMetric(*metric) {
		reqPoints, reqSummary, err := history.Series(records, "request_rate")
		if err != nil {
			log.Fatalf("series: %v", err)
		}
		errPoints, errSummary, err := history.Series(records, "error_rate")
		if err != nil {
			log.Fatalf("series: %v", err)
		}
		fmt.Println()
		fmt.Println(renderMultiTrendSummaryLocalized(*since, []history.Summary{reqSummary, errSummary}, lang))
		if len(reqPoints) == 0 {
			fmt.Printf("%s: no request_rate points in last %s\n", tr(lang, "Trend"), *since)
			return
		}
		fmt.Println()
		fmt.Println(renderRequestErrorChartLocalized(reqPoints, errPoints, *since, *width, *height, lang))
		return
	}

	points, summary, err := history.Series(records, *metric)
	if err != nil {
		log.Fatalf("series: %v", err)
	}
	fmt.Println()
	fmt.Println(renderTrendSummaryLocalized(summary, *since, lang))
	if len(points) == 0 {
		fmt.Printf("%s: no points for metric %q in last %s\n", tr(lang, "Trend"), *metric, *since)
		return
	}
	fmt.Println()
	fmt.Println(renderLineChartLocalized(points, summary, *since, *width, *height, lang))
}

func runCmd(args []string) {
	cli.RunDaemon(args, resolveConfigPath(""))
}

func onceCmd(args []string) {
	cli.RunOnce(args, resolveConfigPath(""))
}

func switchCmd(args []string) {
	fs := flag.NewFlagSet("switch", flag.ExitOnError)
	configPath := fs.String("config", resolveConfigPath(""), "config file")
	apply := fs.Bool("apply", false, "update hosts/config and restart cloudflared")
	protocol := fs.String("protocol", "", "auto, quic, or http2")
	switchMode := fs.String("mode", "", "hot or restart")
	top := fs.Int("top", 0, "override top_n when probing")
	ipsRaw := fs.String("ips", "", "comma or space separated IPs to apply; skips probing")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *top > 0 {
		cfg.Edge.TopN = *top
	}
	if *protocol != "" {
		cfg.Cloudflared.Protocol = *protocol
	}
	if *switchMode != "" {
		cfg.Switching.Strategy = *switchMode
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	ips, err := parseSwitchIPs(*ipsRaw)
	if err != nil {
		log.Fatalf("invalid --ips: %v", err)
	}
	if !*apply {
		cfg.Runtime.DryRun = true
	}
	decision, sw, err := daemon.SwitchNow(context.Background(), cfg, ips, *apply)
	if err != nil {
		daemon.PrintDecision(decision, sw)
		log.Fatalf("switch failed: %v", err)
	}
	daemon.PrintDecision(decision, sw)
}

func parseSwitchIPs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	seen := map[string]bool{}
	var ips []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, err
		}
		ip := addr.String()
		if !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no valid IPs")
	}
	return ips, nil
}

func discoverCmd(args []string) {
	cli.RunDiscover(args)
}

func probeCmd(args []string) {
	cli.RunProbe(args, resolveConfigPath(""))
}

func installCmd(args []string) {
	cli.RunInstall(args, cli.InstallDefaults{
		ConfigPath:          config.DefaultConfigPath,
		BinaryPath:          config.DefaultBinaryPath,
		UnitPath:            config.DefaultUnitPath(),
		IncludeEmergencyRTT: true,
	})
}

func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat(config.DefaultConfigPath); err == nil {
		return config.DefaultConfigPath
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
	return path
}

func hostMappingEdges(current map[string]string, hostnames []string) []cloudflared.EdgeConnection {
	conns := make([]cloudflared.EdgeConnection, 0, len(hostnames))
	for _, h := range hostnames {
		ip := current[h]
		if ip == "" {
			continue
		}
		conns = append(conns, cloudflared.EdgeConnection{
			Local:  "hosts file fallback",
			Remote: h,
			IP:     ip,
			Line:   ip + " " + h,
		})
	}
	return conns
}

func renderStatusSummary(configPath string, cfg config.Config, historyPath, metric, since string, endpoint slots.ActiveEndpoint, ready cloudflared.Ready, readyErr error, metrics cloudflared.Metrics, metricsErr error, records []history.Record, latest *history.Record, lang string) string {
	retention := tr(lang, "disabled")
	if cfg.Runtime.HistoryRetentionDays > 0 {
		retention = fmt.Sprintf("%dd", cfg.Runtime.HistoryRetentionDays)
	}
	emergencyRTT := tr(lang, "disabled")
	if cfg.Switching.EmergencyRTTThresholdMS > 0 {
		emergencyRTT = fmt.Sprintf("%.0f ms", cfg.Switching.EmergencyRTTThresholdMS)
	}
	rows := []prettytable.Row{}
	add := func(section, item, state, value string) {
		rows = append(rows, prettytable.Row{tr(lang, section), tr(lang, item), stateText(lang, state), value})
	}
	add("Config", "Config", "INFO", configPath)
	add("Config", "Protocol", "INFO", cfg.Cloudflared.Protocol)
	add("Config", "Switch Strategy", "INFO", cfg.Switching.Strategy)
	add("Config", "Emergency RTT", "INFO", emergencyRTT)
	add("Config", "Metrics", "INFO", cfg.Cloudflared.MetricsURL)
	add("Config", "Ready", "INFO", cfg.Cloudflared.ReadyURL)
	add("Config", "History", "INFO", historyPath)
	add("Config", "Sample Interval", "INFO", (time.Duration(cfg.Switching.ProbeIntervalSeconds) * time.Second).String())
	add("Config", "History Retention", "INFO", retention)
	add("Config", "Window", "INFO", since)
	add("Config", "Metric", "INFO", metric)

	st := endpoint.State
	active := endpoint.Slot
	add("Slots", "Active slot", "ACTIVE", fmt.Sprintf("%s %s %s %s", emptyDash(active.Name), kv(lang, "service", emptyDash(active.Service)), kv(lang, "metrics", emptyDash(endpoint.MetricsURL)), kv(lang, "source", emptyDash(endpoint.Source))))
	add("Slots", "Green slot", slotStateLabel(active.Name == slots.Green), fmt.Sprintf("%s %s %s", kv(lang, "service", emptyDash(st.Green.Service)), kv(lang, "metrics", emptyDash(st.Green.MetricsURL)), kv(lang, "detail", emptyDash(st.LastResult))))
	add("Slots", "Blue slot", slotStateLabel(active.Name == slots.Blue), fmt.Sprintf("%s %s %s", kv(lang, "service", emptyDash(st.Blue.Service)), kv(lang, "metrics", emptyDash(st.Blue.MetricsURL)), kv(lang, "detail", emptyDash(st.LastMessage))))

	if readyErr != nil {
		add("Health", "Ready", "UNKNOWN", readyErr.Error())
	} else {
		add("Health", "Ready", statusLabel(ready.ReadyConnections >= 2), fmt.Sprintf("connections=%d connector=%s", ready.ReadyConnections, emptyDash(ready.ConnectorID)))
	}
	if metricsErr != nil {
		add("Health", "Metrics", "UNKNOWN", metricsErr.Error())
	} else {
		add("Health", "HA", statusLabel(metrics.HAConnections >= 2), fmt.Sprintf("ha_connections=%d", metrics.HAConnections))
		add("Health", "Traffic", trafficLabel(metrics.ConcurrentRequests), fmt.Sprintf("concurrent=%d total_requests=%.0f", metrics.ConcurrentRequests, metrics.TotalRequests))
		add("Health", "Errors", errorLabel(metrics.RequestErrors), fmt.Sprintf("request_errors=%.0f", metrics.RequestErrors))

		delta := performanceFromHistory(records)
		add("Performance", "Requests", activityLabel(delta.RequestRate), fmt.Sprintf("%s, +%.0f %s, total=%.0f", formatPerSecond(delta.RequestRate, "req"), delta.RequestDelta, tr(lang, "last interval"), metrics.TotalRequests))
		add("Performance", "Errors", statusLabel(delta.ErrorDelta == 0), fmt.Sprintf("%s, +%.0f %s, total=%.0f", formatPerSecond(delta.ErrorRate, "err"), delta.ErrorDelta, tr(lang, "last interval"), metrics.RequestErrors))
		add("Performance", "HTTP Codes", statusLabel(delta.Response5xxDelta == 0), fmt.Sprintf("2xx=%.0f 3xx=%.0f 4xx=%.0f 5xx=%.0f, delta_5xx=%.0f", metrics.Response2xx, metrics.Response3xx, metrics.Response4xx, metrics.Response5xx, delta.Response5xxDelta))
		add("Performance", "Connect Latency", availabilityLabel(metrics.ProxyConnectLatencyHits > 0), fmt.Sprintf("avg=%s samples=%.0f", formatMS(delta.ProxyConnectAvgMS), metrics.ProxyConnectLatencyHits))
		add("Performance", "Sessions", activityLabel(metrics.TCPActiveSessions+metrics.UDPActiveSessions), fmt.Sprintf("tcp_active=%.0f tcp_total=%.0f udp_active=%.0f udp_total=%.0f", metrics.TCPActiveSessions, metrics.TCPTotalSessions, metrics.UDPActiveSessions, metrics.UDPTotalSessions))
		add("Performance", "Runtime", "OK", fmt.Sprintf("rss=%s heap=%s goroutines=%.0f threads=%.0f cpu=%s", formatBytes(metrics.ProcessRSSBytes), formatBytes(metrics.GoHeapAllocBytes), metrics.GoGoroutines, metrics.GoThreads, formatPercent(delta.CPUPercent)))
		add("Performance", "Network", activityLabel(delta.NetworkRxRate+delta.NetworkTxRate), fmt.Sprintf("rx=%s (%s) tx=%s (%s)", formatBytes(metrics.ProcessNetworkRxBytes), formatBytesPerSecond(delta.NetworkRxRate), formatBytes(metrics.ProcessNetworkTxBytes), formatBytesPerSecond(delta.NetworkTxRate)))
		if delta.HasLast {
			add("Performance", "Sample Gap", "INFO", delta.Elapsed.Round(time.Second).String())
		} else {
			add("Performance", "Sample Gap", "INFO", tr(lang, "need two history samples for rates"))
		}
	}

	if latest != nil {
		add("Latest Sample", "Time", "INFO", formatTime(latest.Time))
		add("Latest Sample", "Effective Protocol", "INFO", emptyDash(latest.EffectiveProtocol))
		add("Latest Sample", "Top IP", "INFO", emptyDash(latest.TopIP))
		add("Latest Sample", "Top RTT", "INFO", formatMS(latest.TopMedianMS))
		add("Latest Sample", "Ready / HA", statusLabel(latest.ReadyConnections >= 2 && latest.HAConnections >= 2), fmt.Sprintf("%d / %d", latest.ReadyConnections, latest.HAConnections))
		add("Latest Sample", "Requests / Errors", "INFO", fmt.Sprintf("%.0f / %.0f", latest.TotalRequests, latest.RequestErrors))
		add("Latest Sample", "HTTP 2xx/3xx/4xx/5xx", statusLabel(latest.Response5xx == 0), fmt.Sprintf("%.0f / %.0f / %.0f / %.0f", latest.Response2xx, latest.Response3xx, latest.Response4xx, latest.Response5xx))
		add("Decision", "Idle", boolState(lang, latest.Idle), yesNoLang(lang, latest.Idle))
		add("Decision", "Degraded", boolState(lang, !latest.Degraded), yesNoLang(lang, latest.Degraded))
		add("Decision", "Should Switch", boolState(lang, !latest.ShouldSwitch), yesNoLang(lang, latest.ShouldSwitch))
		add("Decision", "Switch Applied", boolState(lang, !latest.SwitchApplied), yesNoLang(lang, latest.SwitchApplied))
		add("Decision", "Reason", "INFO", emptyDash(latest.Reason))
	} else {
		add("Latest Sample", "History", "UNKNOWN", fmt.Sprintf("%s %s", tr(lang, "no records in last"), since))
	}
	return renderTable(tr(lang, "Status Summary"), []interface{}{tr(lang, "Section"), tr(lang, "Item"), tr(lang, "State"), tr(lang, "Value")}, rows)
}

func slotStateLabel(active bool) string {
	if active {
		return "ACTIVE"
	}
	return "STANDBY"
}

func renderEdgesLocalized(conns []cloudflared.EdgeConnection, err error, lang string) string {
	if err != nil {
		return renderTable(tr(lang, "Current Edges"), []interface{}{tr(lang, "State"), tr(lang, "Detail")}, []prettytable.Row{
			{stateText(lang, "UNKNOWN"), err.Error()},
		})
	}
	if len(conns) == 0 {
		return renderTable(tr(lang, "Current Edges"), []interface{}{tr(lang, "State"), tr(lang, "Detail")}, []prettytable.Row{
			{stateText(lang, "EMPTY"), tr(lang, "no cloudflared :7844 sockets found")},
		})
	}
	rows := make([]prettytable.Row, 0, len(conns))
	for i, conn := range conns {
		rows = append(rows, prettytable.Row{i + 1, conn.IP, conn.Remote, conn.Local})
	}
	return renderTable(tr(lang, "Current Edges"), []interface{}{"#", "IP", tr(lang, "Remote"), tr(lang, "Local")}, rows)
}

func renderEdgeComparisonLocalized(r history.Record, degradedFactor float64, lang string) string {
	rows := []prettytable.Row{}
	best := bestMedianMS(r)
	topRows := r.TopProbeResults
	if len(topRows) == 0 && r.TopIP != "" {
		topRows = []history.IPProbe{{
			IP:       r.TopIP,
			MedianMS: r.TopMedianMS,
		}}
	}
	for i, p := range topRows {
		rows = append(rows, edgeComparisonRowLocalized(fmt.Sprintf("%s #%d", tr(lang, "TOP"), i+1), "TOP", p, best, degradedFactor, lang))
	}
	currentRows := r.CurrentProbeResults
	if len(currentRows) == 0 && len(r.CurrentIPs) > 0 {
		for _, ip := range r.CurrentIPs {
			currentRows = append(currentRows, history.IPProbe{IP: ip})
		}
	}
	for i, p := range currentRows {
		rows = append(rows, edgeComparisonRowLocalized(fmt.Sprintf("%s #%d", tr(lang, "CURRENT"), i+1), "CURRENT", p, best, degradedFactor, lang))
	}
	if len(rows) == 0 {
		return renderTable(tr(lang, "Edge Comparison"), []interface{}{tr(lang, "Role"), tr(lang, "State"), "IP", tr(lang, "Median"), tr(lang, "OK/Fail"), tr(lang, "Vs Best"), tr(lang, "Score")}, []prettytable.Row{
			{stateText(lang, "EMPTY"), stateText(lang, "UNKNOWN"), tr(lang, "no probe comparison in latest sample"), "-", "-", "-", "-"},
		})
	}
	return renderTable(tr(lang, "Edge Comparison"), []interface{}{tr(lang, "Role"), tr(lang, "State"), "IP", tr(lang, "Median"), tr(lang, "OK/Fail"), tr(lang, "Vs Best"), tr(lang, "Score")}, rows)
}

func edgeComparisonRowLocalized(role, kind string, p history.IPProbe, best, degradedFactor float64, lang string) prettytable.Row {
	state := "UNKNOWN"
	switch {
	case p.MedianMS <= 0 && p.OK == 0 && p.Fail == 0:
		state = "UNKNOWN"
	case kind == "TOP":
		state = "BEST"
		if p.IP != "" && best > 0 && p.MedianMS > best {
			state = "TOP"
		}
	case p.OK == 0 && p.Fail > 0:
		state = "FAIL"
	case best > 0 && degradedFactor > 0 && p.MedianMS > best*degradedFactor:
		state = "SLOW"
	default:
		state = "OK"
	}
	return prettytable.Row{
		role,
		stateText(lang, state),
		emptyDash(p.IP),
		formatMS(p.MedianMS),
		okFailLabel(p.OK, p.Fail),
		vsBestLabel(p.MedianMS, best),
		scoreLabel(p.Score),
	}
}

func bestMedianMS(r history.Record) float64 {
	best := r.TopMedianMS
	for _, p := range r.TopProbeResults {
		if p.MedianMS > 0 && (best <= 0 || p.MedianMS < best) {
			best = p.MedianMS
		}
	}
	if best > 0 {
		return best
	}
	for _, p := range r.CurrentProbeResults {
		if p.MedianMS > 0 && (best <= 0 || p.MedianMS < best) {
			best = p.MedianMS
		}
	}
	return best
}

func okFailLabel(ok, fail int) string {
	if ok == 0 && fail == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", ok, fail)
}

func scoreLabel(score float64) string {
	if score <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", score)
}

func vsBestLabel(ms, best float64) string {
	if ms <= 0 || best <= 0 {
		return "-"
	}
	if ms <= best {
		if math.Abs(ms-best) < 0.005 {
			return "best"
		}
		return fmt.Sprintf("-%.2f ms / %.1fx", best-ms, best/ms)
	}
	return fmt.Sprintf("+%.2f ms / %.1fx", ms-best, ms/best)
}

func renderTrendSummaryLocalized(sum history.Summary, since string, lang string) string {
	if sum.Count == 0 {
		return renderTable(tr(lang, "Trend"), []interface{}{tr(lang, "Window"), tr(lang, "Metric"), tr(lang, "Points")}, []prettytable.Row{
			{since, sum.Metric, 0},
		})
	}
	return renderTable(tr(lang, "Trend"), []interface{}{tr(lang, "Window"), tr(lang, "Metric"), tr(lang, "Points"), tr(lang, "Latest"), tr(lang, "Min"), tr(lang, "Avg"), tr(lang, "Max"), tr(lang, "From"), tr(lang, "To")}, []prettytable.Row{
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

func renderMultiTrendSummaryLocalized(since string, summaries []history.Summary, lang string) string {
	rows := make([]prettytable.Row, 0, len(summaries))
	for _, sum := range summaries {
		if sum.Count == 0 {
			rows = append(rows, prettytable.Row{since, sum.Metric, 0, "-", "-", "-", "-", "-", "-"})
			continue
		}
		rows = append(rows, prettytable.Row{
			since,
			sum.Metric,
			sum.Count,
			fmt.Sprintf("%.2f", sum.Latest),
			fmt.Sprintf("%.2f", sum.Min),
			fmt.Sprintf("%.2f", sum.Avg),
			fmt.Sprintf("%.2f", sum.Max),
			formatTime(sum.From),
			formatTime(sum.To),
		})
	}
	return renderTable(tr(lang, "Trend"), []interface{}{tr(lang, "Window"), tr(lang, "Metric"), tr(lang, "Points"), tr(lang, "Latest"), tr(lang, "Min"), tr(lang, "Avg"), tr(lang, "Max"), tr(lang, "From"), tr(lang, "To")}, rows)
}

func renderLineChartLocalized(points []history.Point, sum history.Summary, since string, width, height int, lang string) string {
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
		asciigraph.Caption(fmt.Sprintf("%s %s %s", sum.Metric, tr(lang, "over"), since)),
	}
	if sum.Min == sum.Max {
		opts = append(opts, asciigraph.LowerBound(sum.Min-1), asciigraph.UpperBound(sum.Max+1))
	}
	return asciigraph.Plot(values, opts...)
}

func renderRequestErrorChartLocalized(requestPoints, errorPoints []history.Point, since string, width, height int, lang string) string {
	requests := pointValues(requestPoints)
	errors := pointValues(errorPoints)
	if len(errors) != len(requests) {
		errors = alignSeries(errors, len(requests))
	}
	width = clamp(width, 20, 160)
	height = clamp(height, 4, 40)
	return asciigraph.PlotMany(
		[][]float64{requests, errors},
		asciigraph.Width(width),
		asciigraph.Height(height),
		asciigraph.Precision(2),
		asciigraph.SeriesColors(asciigraph.Green, asciigraph.Red),
		asciigraph.SeriesLegends(tr(lang, "request_rate req/s"), tr(lang, "error_rate err/s")),
		asciigraph.Caption(fmt.Sprintf("%s %s", tr(lang, "request_rate and error_rate over"), since)),
	)
}

func pointValues(points []history.Point) []float64 {
	values := make([]float64, 0, len(points))
	for _, point := range points {
		values = append(values, point.Value)
	}
	return values
}

func alignSeries(values []float64, want int) []float64 {
	out := make([]float64, want)
	copy(out, values)
	return out
}

func isRequestRateMetric(metric string) bool {
	metric = strings.TrimSpace(strings.ToLower(metric))
	return metric == "" || metric == "request_rate" || metric == "requests_rate" || metric == "rps"
}

func renderTable(title string, header []interface{}, rows []prettytable.Row) string {
	t := prettytable.NewWriter()
	t.SetTitle(title)
	t.SetStyle(prettytable.StyleDefault)
	t.AppendHeader(prettytable.Row(header))
	t.AppendRows(rows)
	return t.Render()
}

func normalizeStatusLang(raw string, zh bool) string {
	if zh {
		return "zh"
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "zh", "zh-cn", "cn", "chinese":
		return "zh"
	default:
		return "en"
	}
}

func tr(lang, key string) string {
	if lang != "zh" {
		return key
	}
	if value, ok := zhLabels[key]; ok {
		return value
	}
	return key
}

var zhLabels = map[string]string{
	"Active slot":                          "当前槽位",
	"Avg":                                  "平均",
	"Blue slot":                            "Blue 槽位",
	"Config":                               "配置",
	"Connect Latency":                      "连接延迟",
	"CURRENT":                              "当前",
	"Current Edges":                        "当前边缘 IP",
	"Decision":                             "切换决策",
	"Degraded":                             "连接变差",
	"Detail":                               "详情",
	"disabled":                             "关闭",
	"Effective Protocol":                   "实际协议",
	"Edge Comparison":                      "边缘 IP 对比",
	"Emergency RTT":                        "应急 RTT",
	"error_rate err/s":                     "错误率 err/s",
	"Errors":                               "错误",
	"From":                                 "开始",
	"Green slot":                           "Green 槽位",
	"HA":                                   "高可用",
	"Health":                               "健康",
	"History":                              "历史",
	"History Retention":                    "历史保留",
	"HTTP Codes":                           "HTTP 状态码",
	"HTTP 2xx/3xx/4xx/5xx":                 "HTTP 2xx/3xx/4xx/5xx",
	"Idle":                                 "空闲",
	"Item":                                 "项目",
	"Latest":                               "最新",
	"Latest Sample":                        "最近采样",
	"Local":                                "本地",
	"Max":                                  "最大",
	"Median":                               "中位数",
	"Metric":                               "指标",
	"Metrics":                              "监控地址",
	"Min":                                  "最小",
	"Network":                              "网络",
	"no cloudflared :7844 sockets found":   "没有发现 cloudflared 的 :7844 连接",
	"no probe comparison in latest sample": "最近采样没有 IP 探测对比",
	"no records in last":                   "最近没有记录",
	"OK/Fail":                              "成功/失败",
	"over":                                 "过去",
	"Performance":                          "性能",
	"Points":                               "点数",
	"Protocol":                             "协议",
	"Ready":                                "就绪",
	"Ready / HA":                           "就绪 / 高可用",
	"Reason":                               "原因",
	"Remote":                               "远端",
	"request_rate and error_rate over":     "请求率与错误率，时间窗口",
	"request_rate req/s":                   "请求率 req/s",
	"Requests":                             "请求",
	"Requests / Errors":                    "请求 / 错误",
	"Role":                                 "角色",
	"Runtime":                              "运行时",
	"Sample Gap":                           "采样间隔",
	"Sample Interval":                      "采样周期",
	"Score":                                "评分",
	"Section":                              "分组",
	"Sessions":                             "会话",
	"Should Switch":                        "应切换",
	"Slots":                                "槽位",
	"Source":                               "来源",
	"State":                                "状态",
	"Status Summary":                       "状态总览",
	"Switch Applied":                       "已执行切换",
	"Switch Strategy":                      "切换策略",
	"Time":                                 "时间",
	"To":                                   "结束",
	"TOP":                                  "候选",
	"Top IP":                               "最佳 IP",
	"Top RTT":                              "最佳 RTT",
	"Traffic":                              "流量",
	"Trend":                                "趋势",
	"Value":                                "值",
	"Vs Best":                              "相比最佳",
	"Window":                               "窗口",
	"detail":                               "详情",
	"last interval":                        "上一周期",
	"metrics":                              "监控",
	"need two history samples for rates":   "需要至少两次历史采样才能计算速率",
	"service":                              "服务",
	"source":                               "来源",
}

func stateText(lang, state string) string {
	if lang != "zh" {
		return state
	}
	switch state {
	case "ACTIVE":
		return "活跃"
	case "BEST":
		return "最佳"
	case "EMPTY":
		return "空"
	case "FAIL":
		return "失败"
	case "IDLE":
		return "空闲"
	case "INFO":
		return "信息"
	case "N/A":
		return "无数据"
	case "OK":
		return "正常"
	case "SEEN":
		return "有记录"
	case "SLOW":
		return "偏慢"
	case "STANDBY":
		return "待命"
	case "TOP":
		return "候选"
	case "UNKNOWN":
		return "未知"
	case "WARN":
		return "警告"
	default:
		return state
	}
}

func boolState(lang string, ok bool) string {
	if ok {
		return stateText(lang, "OK")
	}
	return stateText(lang, "WARN")
}

func yesNoLang(lang string, v bool) string {
	if lang == "zh" {
		if v {
			return "是"
		}
		return "否"
	}
	return yesNo(v)
}

func kv(lang, key, value string) string {
	return fmt.Sprintf("%s=%s", tr(lang, key), value)
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

func performanceFromHistory(records []history.Record) performanceDelta {
	d := performanceDelta{}
	if len(records) < 2 {
		if len(records) == 1 && records[0].ProxyConnectLatencyHits > 0 {
			d.ProxyConnectAvgMS = records[0].ProxyConnectLatencySum / records[0].ProxyConnectLatencyHits
		}
		return d
	}
	last := records[len(records)-1]
	prev := records[len(records)-2]
	elapsed := last.Time.Sub(prev.Time)
	if elapsed <= 0 {
		return d
	}
	seconds := elapsed.Seconds()
	d.HasLast = true
	d.Elapsed = elapsed
	d.RequestDelta = nonNegativeDelta(last.TotalRequests, prev.TotalRequests)
	d.RequestRate = d.RequestDelta / seconds
	d.ErrorDelta = nonNegativeDelta(last.RequestErrors, prev.RequestErrors)
	d.ErrorRate = d.ErrorDelta / seconds
	if prev.ResponseByCode != nil || prev.Response2xx != 0 || prev.Response3xx != 0 || prev.Response4xx != 0 || prev.Response5xx != 0 {
		d.Response5xxDelta = nonNegativeDelta(last.Response5xx, prev.Response5xx)
	}
	if last.ProxyConnectLatencyHits > prev.ProxyConnectLatencyHits {
		d.ProxyConnectAvgMS = nonNegativeDelta(last.ProxyConnectLatencySum, prev.ProxyConnectLatencySum) / nonNegativeDelta(last.ProxyConnectLatencyHits, prev.ProxyConnectLatencyHits)
	} else if last.ProxyConnectLatencyHits > 0 {
		d.ProxyConnectAvgMS = last.ProxyConnectLatencySum / last.ProxyConnectLatencyHits
	}
	if prev.ProcessCPUSeconds > 0 {
		d.CPUPercent = 100 * nonNegativeDelta(last.ProcessCPUSeconds, prev.ProcessCPUSeconds) / seconds
	}
	if prev.ProcessNetworkRxBytes > 0 {
		d.NetworkRxRate = nonNegativeDelta(last.ProcessNetworkRxBytes, prev.ProcessNetworkRxBytes) / seconds
	}
	if prev.ProcessNetworkTxBytes > 0 {
		d.NetworkTxRate = nonNegativeDelta(last.ProcessNetworkTxBytes, prev.ProcessNetworkTxBytes) / seconds
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

func usage() {
	fmt.Fprintf(os.Stderr, "usage: tunnelflux status|run|once|switch|discover|probe|install|version [flags]\n")
}
