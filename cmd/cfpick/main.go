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

	"github.com/kayphoon/cfedgepickd/internal/cloudflared"
	"github.com/kayphoon/cfedgepickd/internal/config"
	"github.com/kayphoon/cfedgepickd/internal/daemon"
	"github.com/kayphoon/cfedgepickd/internal/discover"
	"github.com/kayphoon/cfedgepickd/internal/history"
	"github.com/kayphoon/cfedgepickd/internal/install"
	"github.com/kayphoon/cfedgepickd/internal/probe"
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

	fmt.Printf("cfpick status\n")
	fmt.Printf("config=%s protocol=%s history=%s\n", path, cfg.Cloudflared.Protocol, cfg.Runtime.HistoryFile)

	if ready, err := cloudflared.FetchReady(ctx, cfg.Cloudflared.ReadyURL); err == nil {
		fmt.Printf("ready: connections=%d connector=%s\n", ready.ReadyConnections, ready.ConnectorID)
	} else {
		fmt.Printf("ready: unavailable (%v)\n", err)
	}
	if metrics, err := cloudflared.FetchMetrics(ctx, cfg.Cloudflared.MetricsURL); err == nil {
		fmt.Printf("metrics: ha=%d concurrent=%d requests=%.0f errors=%.0f\n",
			metrics.HAConnections,
			metrics.ConcurrentRequests,
			metrics.TotalRequests,
			metrics.RequestErrors,
		)
	} else {
		fmt.Printf("metrics: unavailable (%v)\n", err)
	}
	if conns, err := cloudflared.CurrentEdges(ctx, cfg.Edge.Port); err == nil && len(conns) > 0 {
		fmt.Printf("edges: %s\n", strings.Join(edgeRemotes(conns), ", "))
	}

	historyPath := resolveHistoryPath(cfg.Runtime.HistoryFile)
	records, err := history.ReadSince(historyPath, time.Now().Add(-window))
	if err != nil {
		log.Fatalf("read history: %v", err)
	}
	if len(records) == 0 {
		fmt.Printf("history: no records in last %s at %s\n", *since, historyPath)
		return
	}
	latest := records[len(records)-1]
	fmt.Printf("latest: effective=%s top=%s rtt=%.2fms ready=%d ha=%d reason=%s\n",
		latest.EffectiveProtocol,
		latest.TopIP,
		latest.TopMedianMS,
		latest.ReadyConnections,
		latest.HAConnections,
		latest.Reason,
	)

	points, summary, err := history.Series(records, *metric)
	if err != nil {
		log.Fatalf("series: %v", err)
	}
	out, err := history.RenderASCII(points, summary, *width, *height)
	if err != nil {
		fmt.Printf("graph: %v\n", err)
		return
	}
	fmt.Print(out)
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
