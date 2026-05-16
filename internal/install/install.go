package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kayphoon/cfpick/internal/config"
	"github.com/kayphoon/cfpick/internal/discover"
	"github.com/kayphoon/cfpick/internal/probe"
)

type Options struct {
	Apply    bool
	Protocol string
	Config   string
	Binary   string
	UnitPath string
}

type Report struct {
	Discover discover.Report `json:"discover"`
	Probe    probe.Report    `json:"probe"`
	Config   config.Config   `json:"config"`
	Unit     string          `json:"unit"`
	Applied  bool            `json:"applied"`
	Notes    []string        `json:"notes"`
}

func Run(ctx context.Context, opts Options) (Report, error) {
	dr, err := discover.Run(ctx)
	if err != nil {
		return Report{}, err
	}
	cfg := dr.Config.WithDefaults()
	if opts.Protocol != "" {
		cfg.Cloudflared.Protocol = opts.Protocol
	}
	if err := cfg.Validate(); err != nil {
		return Report{Discover: dr, Config: cfg}, err
	}
	pr, err := probe.Run(ctx, cfg, probe.Mode(cfg.Cloudflared.Protocol))
	cfg.Runtime.DryRun = true
	unit := RenderUnit(opts.Binary, opts.Config, cfg.Cloudflared.Service)
	rep := Report{Discover: dr, Probe: pr, Config: cfg, Unit: unit}
	if !opts.Apply {
		rep.Notes = append(rep.Notes, "dry-run only; no files written")
		return rep, err
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
		unitPath = config.DefaultUnitPath
	}
	if err := os.WriteFile(unitPath, []byte(RenderUnit(opts.Binary, configPath, cfg.Cloudflared.Service)), 0644); err != nil {
		return rep, err
	}
	_ = exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
	rep.Applied = true
	rep.Config = cfg
	rep.Unit = RenderUnit(opts.Binary, configPath, cfg.Cloudflared.Service)
	rep.Notes = append(rep.Notes, "wrote "+configPath, "wrote "+unitPath)
	return rep, err
}

func RenderUnit(binary, configPath, cloudflaredService string) string {
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
		"Description=cfpick Cloudflare edge IP picker for cloudflared",
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
