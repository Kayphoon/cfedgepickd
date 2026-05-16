package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kayphoon/cfpick/internal/config"
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
	case "discover":
		discoverCmd(os.Args[2:])
	case "probe":
		probeCmd(os.Args[2:])
	case "graph":
		graphCmd(os.Args[2:])
	case "install":
		installCmd(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func graphCmd(args []string) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	configPath := fs.String("config", "/etc/cfedgepickd/config.json", "config file")
	metric := fs.String("metric", "rtt", "rtt, ready, ha, concurrent, request_delta, error_delta, degraded, idle")
	since := fs.String("since", "24h", "history window, for example 24h or 7d")
	width := fs.Int("width", 80, "graph width")
	height := fs.Int("height", 12, "graph height")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	window, err := parseWindow(*since)
	if err != nil {
		log.Fatalf("invalid --since: %v", err)
	}
	records, err := history.ReadSince(cfg.Runtime.HistoryFile, time.Now().Add(-window))
	if err != nil {
		log.Fatalf("read history: %v", err)
	}
	points, summary, err := history.Series(records, *metric)
	if err != nil {
		log.Fatalf("series: %v", err)
	}
	out, err := history.RenderASCII(points, summary, *width, *height)
	if err != nil {
		log.Fatalf("graph: %v", err)
	}
	fmt.Print(out)
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
	configPath := fs.String("config", "", "config file")
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
	configPath := fs.String("config", "/etc/cfedgepickd/config.json", "target config path")
	binary := fs.String("binary", "/usr/local/bin/cfedgepickd", "daemon binary path in systemd unit")
	unit := fs.String("unit", "/etc/systemd/system/cfedgepickd.service", "target systemd unit path")
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

func usage() {
	fmt.Fprintf(os.Stderr, "usage: cfedgepickctl discover|probe|graph|install|version [flags]\n")
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
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}
