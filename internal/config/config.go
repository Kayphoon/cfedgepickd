package config

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

const (
	ProtocolAuto  = "auto"
	ProtocolQUIC  = "quic"
	ProtocolHTTP2 = "http2"

	CloudflaredQUICServerName = "quic.cftunnel.com"
)

var DefaultHostnames = []string{
	"region1.v2.argotunnel.com",
	"region2.v2.argotunnel.com",
	"us-region1.v2.argotunnel.com",
	"us-region2.v2.argotunnel.com",
}

var DefaultCandidateCIDRs = []string{
	"198.41.192.0/24",
	"198.41.193.0/24",
	"198.41.194.0/24",
	"198.41.195.0/24",
	"198.41.196.0/24",
	"198.41.197.0/24",
	"198.41.198.0/24",
	"198.41.199.0/24",
	"198.41.200.0/24",
	"2606:4700:a0::/124",
	"2606:4700:a1::/124",
	"2606:4700:a8::/124",
	"2606:4700:a9::/124",
}

type Config struct {
	Cloudflared CloudflaredConfig `json:"cloudflared"`
	Edge        EdgeConfig        `json:"edge"`
	Switching   SwitchingConfig   `json:"switching"`
	Runtime     RuntimeConfig     `json:"runtime"`
}

type CloudflaredConfig struct {
	Binary     string `json:"binary"`
	Service    string `json:"service"`
	ConfigPath string `json:"config_path"`
	Protocol   string `json:"protocol"`
	MetricsURL string `json:"metrics_url"`
	ReadyURL   string `json:"ready_url"`
	Systemctl  string `json:"systemctl"`
}

type EdgeConfig struct {
	Port          int      `json:"port"`
	Hostnames     []string `json:"hostnames"`
	CandidateFile string   `json:"candidate_file"`
	Candidates    []string `json:"candidates"`
	TopN          int      `json:"top_n"`
	ProbeRounds   int      `json:"probe_rounds"`
	ProbeTimeout  string   `json:"probe_timeout"`
	Concurrency   int      `json:"concurrency"`
	MaxCandidates int      `json:"max_candidates"`
	ServerName    string   `json:"server_name"`
}

type SwitchingConfig struct {
	ProbeIntervalSeconds  int     `json:"probe_interval_seconds"`
	MetricsPollSeconds    int     `json:"metrics_poll_seconds"`
	IdleWindowSeconds     int     `json:"idle_window_seconds"`
	CooldownSeconds       int     `json:"cooldown_seconds"`
	RestartTimeoutSeconds int     `json:"restart_timeout_seconds"`
	MinImprovementRatio   float64 `json:"min_improvement_ratio"`
	DegradedFactor        float64 `json:"degraded_factor"`
	DegradedRounds        int     `json:"degraded_rounds"`
	AllowEmergencySwitch  bool    `json:"allow_emergency_switch"`
	RequireIdleForPlanned bool    `json:"require_idle_for_planned"`
	ApplyProtocolToConfig bool    `json:"apply_protocol_to_config"`
}

type RuntimeConfig struct {
	HostsFile string `json:"hosts_file"`
	BackupDir string `json:"backup_dir"`
	StateFile string `json:"state_file"`
	LogLevel  string `json:"log_level"`
	DryRun    bool   `json:"dry_run"`
}

func Default() Config {
	return Config{
		Cloudflared: CloudflaredConfig{
			Binary:     "cloudflared",
			Service:    "cloudflared",
			ConfigPath: "/etc/cloudflared/config.yml",
			Protocol:   ProtocolAuto,
			MetricsURL: "http://127.0.0.1:20241/metrics",
			ReadyURL:   "http://127.0.0.1:20241/ready",
			Systemctl:  "systemctl",
		},
		Edge: EdgeConfig{
			Port:          7844,
			Hostnames:     append([]string(nil), DefaultHostnames...),
			CandidateFile: "/opt/cftunnel/ip.txt",
			Candidates:    append([]string(nil), DefaultCandidateCIDRs...),
			TopN:          4,
			ProbeRounds:   8,
			ProbeTimeout:  "800ms",
			Concurrency:   128,
			MaxCandidates: 4096,
			ServerName:    CloudflaredQUICServerName,
		},
		Switching: SwitchingConfig{
			ProbeIntervalSeconds:  600,
			MetricsPollSeconds:    5,
			IdleWindowSeconds:     180,
			CooldownSeconds:       3600,
			RestartTimeoutSeconds: 30,
			MinImprovementRatio:   0.35,
			DegradedFactor:        3.0,
			DegradedRounds:        3,
			AllowEmergencySwitch:  true,
			RequireIdleForPlanned: true,
			ApplyProtocolToConfig: true,
		},
		Runtime: RuntimeConfig{
			HostsFile: "/etc/hosts",
			BackupDir: "/var/backups/cfedgepickd",
			StateFile: "/var/lib/cfedgepickd/state.json",
			LogLevel:  "info",
			DryRun:    true,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg.WithDefaults(), nil
}

func (c Config) WithDefaults() Config {
	def := Default()
	if c.Cloudflared.Binary == "" {
		c.Cloudflared.Binary = def.Cloudflared.Binary
	}
	if c.Cloudflared.Service == "" {
		c.Cloudflared.Service = def.Cloudflared.Service
	}
	if c.Cloudflared.ConfigPath == "" {
		c.Cloudflared.ConfigPath = def.Cloudflared.ConfigPath
	}
	if c.Cloudflared.Protocol == "" {
		c.Cloudflared.Protocol = def.Cloudflared.Protocol
	}
	if c.Cloudflared.MetricsURL == "" {
		c.Cloudflared.MetricsURL = def.Cloudflared.MetricsURL
	}
	if c.Cloudflared.ReadyURL == "" {
		c.Cloudflared.ReadyURL = def.Cloudflared.ReadyURL
	}
	if c.Cloudflared.Systemctl == "" {
		c.Cloudflared.Systemctl = def.Cloudflared.Systemctl
	}
	if c.Edge.Port == 0 {
		c.Edge.Port = def.Edge.Port
	}
	if len(c.Edge.Hostnames) == 0 {
		c.Edge.Hostnames = append([]string(nil), def.Edge.Hostnames...)
	}
	if c.Edge.CandidateFile == "" {
		c.Edge.CandidateFile = def.Edge.CandidateFile
	}
	if len(c.Edge.Candidates) == 0 {
		c.Edge.Candidates = append([]string(nil), def.Edge.Candidates...)
	}
	if c.Edge.TopN == 0 {
		c.Edge.TopN = def.Edge.TopN
	}
	if c.Edge.ProbeRounds == 0 {
		c.Edge.ProbeRounds = def.Edge.ProbeRounds
	}
	if c.Edge.ProbeTimeout == "" {
		c.Edge.ProbeTimeout = def.Edge.ProbeTimeout
	}
	if c.Edge.Concurrency == 0 {
		c.Edge.Concurrency = def.Edge.Concurrency
	}
	if c.Edge.MaxCandidates == 0 {
		c.Edge.MaxCandidates = def.Edge.MaxCandidates
	}
	if c.Edge.ServerName == "" {
		c.Edge.ServerName = c.Edge.Hostnames[0]
	}
	if c.Switching.ProbeIntervalSeconds == 0 {
		c.Switching.ProbeIntervalSeconds = def.Switching.ProbeIntervalSeconds
	}
	if c.Switching.MetricsPollSeconds == 0 {
		c.Switching.MetricsPollSeconds = def.Switching.MetricsPollSeconds
	}
	if c.Switching.IdleWindowSeconds == 0 {
		c.Switching.IdleWindowSeconds = def.Switching.IdleWindowSeconds
	}
	if c.Switching.CooldownSeconds == 0 {
		c.Switching.CooldownSeconds = def.Switching.CooldownSeconds
	}
	if c.Switching.RestartTimeoutSeconds == 0 {
		c.Switching.RestartTimeoutSeconds = def.Switching.RestartTimeoutSeconds
	}
	if c.Switching.MinImprovementRatio == 0 {
		c.Switching.MinImprovementRatio = def.Switching.MinImprovementRatio
	}
	if c.Switching.DegradedFactor == 0 {
		c.Switching.DegradedFactor = def.Switching.DegradedFactor
	}
	if c.Switching.DegradedRounds == 0 {
		c.Switching.DegradedRounds = def.Switching.DegradedRounds
	}
	if c.Runtime.HostsFile == "" {
		c.Runtime.HostsFile = def.Runtime.HostsFile
	}
	if c.Runtime.BackupDir == "" {
		c.Runtime.BackupDir = def.Runtime.BackupDir
	}
	if c.Runtime.StateFile == "" {
		c.Runtime.StateFile = def.Runtime.StateFile
	}
	if c.Runtime.LogLevel == "" {
		c.Runtime.LogLevel = def.Runtime.LogLevel
	}
	return c
}

func (c Config) Validate() error {
	switch c.Cloudflared.Protocol {
	case ProtocolAuto, ProtocolQUIC, ProtocolHTTP2:
	default:
		return errors.New("cloudflared.protocol must be auto, quic, or http2")
	}
	if c.Edge.Port <= 0 || c.Edge.Port > 65535 {
		return errors.New("edge.port must be between 1 and 65535")
	}
	if c.Edge.TopN <= 0 {
		return errors.New("edge.top_n must be positive")
	}
	if len(c.Edge.Hostnames) == 0 {
		return errors.New("edge.hostnames must not be empty")
	}
	if _, err := time.ParseDuration(c.Edge.ProbeTimeout); err != nil {
		return err
	}
	return nil
}

func (c Config) MarshalPretty() ([]byte, error) {
	return json.MarshalIndent(c.WithDefaults(), "", "  ")
}
