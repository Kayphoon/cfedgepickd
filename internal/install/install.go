package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kayphoon/tunnelflux/internal/config"
	"github.com/kayphoon/tunnelflux/internal/discover"
	"github.com/kayphoon/tunnelflux/internal/probe"
	"github.com/kayphoon/tunnelflux/internal/switcher"
)

const (
	installProbeMaxCandidates = 256
	installProbeRounds        = 2
	installProbeTimeout       = "400ms"
	installProbeConcurrency   = 128
)

type Options struct {
	Apply                   bool
	Protocol                string
	Config                  string
	Binary                  string
	UnitPath                string
	EmergencyRTTThresholdMS float64
}

type Report struct {
	Discover discover.Report  `json:"discover"`
	Probe    probe.Report     `json:"probe"`
	Config   config.Config    `json:"config"`
	Unit     string           `json:"unit"`
	Switch   *switcher.Result `json:"switch,omitempty"`
	Applied  bool             `json:"applied"`
	Notes    []string         `json:"notes"`
}

type initialPinApplyFunc func(context.Context, config.Config, []string, string) (switcher.Result, error)

func Run(ctx context.Context, opts Options) (Report, error) {
	dr, err := discover.Run(ctx)
	if err != nil {
		return Report{}, err
	}
	cfg := dr.Config.WithDefaults()
	if opts.Protocol != "" {
		cfg.Cloudflared.Protocol = opts.Protocol
	}
	if opts.EmergencyRTTThresholdMS != 0 {
		cfg.Switching.EmergencyRTTThresholdMS = opts.EmergencyRTTThresholdMS
	}
	if err := cfg.Validate(); err != nil {
		return Report{Discover: dr, Config: cfg}, err
	}
	pr, probeErr := probe.Run(ctx, probeConfigForInstall(cfg), probe.Mode(cfg.Cloudflared.Protocol))
	cfg.Runtime.DryRun = true
	unit := RenderService(opts.Binary, opts.Config, cfg.Cloudflared.Service)
	rep := Report{Discover: dr, Probe: pr, Config: cfg, Unit: unit}
	rep.Notes = append(rep.Notes, "installer probe uses a fast sample; daemon keeps full configured probe settings")
	if !opts.Apply {
		rep.Notes = append(rep.Notes, "dry-run only; no files written")
		return rep, probeErr
	}
	if probeErr != nil {
		return rep, probeErr
	}

	configPath := opts.Config
	if configPath == "" {
		configPath = config.DefaultConfigPath
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return rep, err
	}
	cfg.Runtime.DryRun = false
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, append(data, '\n'), 0644); err != nil {
		return rep, err
	}
	unitPath := opts.UnitPath
	if unitPath == "" {
		unitPath = config.DefaultUnitPath()
	}
	if err := os.WriteFile(unitPath, []byte(RenderService(opts.Binary, configPath, cfg.Cloudflared.Service)), 0644); err != nil {
		return rep, err
	}
	if runtime.GOOS != "darwin" {
		_ = exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
	}
	rep.Applied = true
	rep.Config = cfg
	rep.Unit = RenderService(opts.Binary, configPath, cfg.Cloudflared.Service)
	rep.Notes = append(rep.Notes, "wrote "+configPath, "wrote "+unitPath)
	sw, err := applyInitialPin(ctx, cfg, pr, switcher.Apply)
	rep.Switch = &sw
	if err != nil {
		return rep, err
	}
	rep.Notes = append(rep.Notes, "applied initial edge pin")
	return rep, nil
}

func applyInitialPin(ctx context.Context, cfg config.Config, pr probe.Report, apply initialPinApplyFunc) (switcher.Result, error) {
	if apply == nil {
		apply = switcher.Apply
	}
	cfg = cfg.WithDefaults()
	cfg.Runtime.DryRun = false
	ips := initialPinIPs(pr.Top)
	if len(ips) == 0 {
		return switcher.Result{}, fmt.Errorf("initial installer probe produced no healthy edge IPs")
	}
	protocol := pr.EffectiveProtocol
	if protocol == "" {
		protocol = cfg.Cloudflared.Protocol
	}
	return apply(ctx, cfg, ips, protocol)
}

func initialPinIPs(rows []probe.Result) []string {
	seen := map[string]bool{}
	var ips []string
	for _, r := range rows {
		if r.IP == "" || r.OK <= 0 || seen[r.IP] {
			continue
		}
		seen[r.IP] = true
		ips = append(ips, r.IP)
	}
	return ips
}

func probeConfigForInstall(cfg config.Config) config.Config {
	cfg.Edge.MaxCandidates = capPositive(cfg.Edge.MaxCandidates, installProbeMaxCandidates)
	cfg.Edge.ProbeRounds = capPositive(cfg.Edge.ProbeRounds, installProbeRounds)
	cfg.Edge.Concurrency = capPositive(cfg.Edge.Concurrency, installProbeConcurrency)
	cfg.Edge.ProbeTimeout = capDuration(cfg.Edge.ProbeTimeout, installProbeTimeout)
	return cfg
}

func capPositive(v, max int) int {
	if v <= 0 || v > max {
		return max
	}
	return v
}

func capDuration(v, max string) string {
	d, err := time.ParseDuration(v)
	if err != nil {
		return max
	}
	limit, err := time.ParseDuration(max)
	if err != nil {
		return v
	}
	if d <= 0 || d > limit {
		return max
	}
	return v
}

func RenderUnit(binary, configPath, cloudflaredService string) string {
	return RenderSystemdUnit(binary, configPath, cloudflaredService)
}

func RenderService(binary, configPath, cloudflaredService string) string {
	if runtime.GOOS == "darwin" {
		return RenderLaunchdPlist(binary, configPath)
	}
	return RenderSystemdUnit(binary, configPath, cloudflaredService)
}

func RenderSystemdUnit(binary, configPath, cloudflaredService string) string {
	if binary == "" {
		binary = config.DefaultBinaryPath
	}
	if configPath == "" {
		configPath = config.DefaultConfigPath
	}
	if cloudflaredService == "" {
		cloudflaredService = "cloudflared.service"
	}
	if !strings.HasSuffix(cloudflaredService, ".service") {
		cloudflaredService += ".service"
	}
	lines := []string{
		"[Unit]",
		"Description=TunnelFlux Cloudflare edge IP steering for cloudflared",
		fmt.Sprintf("After=network-online.target %s", cloudflaredService),
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s run --config %s", binary, configPath),
		"Restart=on-failure",
		"RestartSec=10s",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}
	return strings.Join(lines, "\n")
}

func RenderLaunchdPlist(binary, configPath string) string {
	if binary == "" {
		binary = config.DefaultBinaryPath
	}
	if configPath == "" {
		configPath = config.DefaultConfigPath
	}
	args := []string{binary, "run", "--config", configPath}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>com.kayphoon.tunnelflux</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range args {
		b.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	b.WriteString("\t<key>StandardOutPath</key>\n\t<string>/var/log/tunnelflux.log</string>\n")
	b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>/var/log/tunnelflux.err.log</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return strings.ReplaceAll(s, "'", "&apos;")
}
