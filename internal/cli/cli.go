package cli

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

	"github.com/Kayphoon/TunnelFlux/internal/config"
	"github.com/Kayphoon/TunnelFlux/internal/daemon"
	"github.com/Kayphoon/TunnelFlux/internal/discover"
	"github.com/Kayphoon/TunnelFlux/internal/install"
	"github.com/Kayphoon/TunnelFlux/internal/probe"
)

type InstallDefaults struct {
	ConfigPath          string
	BinaryPath          string
	UnitPath            string
	IncludeEmergencyRTT bool
}

func RunDaemon(args []string, defaultConfig string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "config file")
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

func RunOnce(args []string, defaultConfig string) {
	fs := flag.NewFlagSet("once", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "config file")
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

func RunDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	pretty := fs.Bool("pretty", true, "pretty JSON")
	_ = fs.Parse(args)
	rep, err := discover.Run(context.Background())
	if err != nil {
		log.Fatalf("discover failed: %v", err)
	}
	PrintJSON(rep, *pretty)
}

func RunProbe(args []string, defaultConfig string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "config file")
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
		PrintJSON(rep, *pretty)
		log.Fatalf("probe failed: %v", err)
	}
	PrintJSON(rep, *pretty)
}

func RunInstall(args []string, defaults InstallDefaults) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	apply := fs.Bool("apply", false, "write config and service definition")
	dryRun := fs.Bool("dry-run", false, "print discovered config and planned writes without changing files")
	protocol := fs.String("protocol", "auto", "auto, quic, or http2")
	configPath := fs.String("config", defaults.ConfigPath, "target config path")
	binary := fs.String("binary", defaults.BinaryPath, "binary path in service definition")
	unit := fs.String("unit", defaults.UnitPath, "target service unit/plist path")
	emergencyRTTMS := fs.Float64("emergency-rtt-ms", 0, "immediate hot-switch threshold in ms; 0 disables")
	pretty := fs.Bool("pretty", true, "pretty JSON")
	_ = fs.Parse(args)
	applyMode := *apply && !*dryRun

	opts := install.Options{
		Apply:    applyMode,
		Protocol: *protocol,
		Config:   *configPath,
		Binary:   *binary,
		UnitPath: *unit,
	}
	if defaults.IncludeEmergencyRTT {
		opts.EmergencyRTTThresholdMS = *emergencyRTTMS
	}
	rep, err := install.Run(context.Background(), opts)
	if err != nil {
		log.Printf("install completed with probe warning/error: %v", err)
	}
	PrintJSON(rep, *pretty)
	if err != nil && applyMode {
		os.Exit(1)
	}
}

func PrintJSON(v any, pretty bool) {
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

func ParseWindow(raw string) (time.Duration, error) {
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
