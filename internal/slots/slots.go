package slots

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kayphoon/cfpick/internal/cloudflared"
	"github.com/kayphoon/cfpick/internal/config"
)

const (
	Green = "green"
	Blue  = "blue"
)

type Slot struct {
	Name       string `json:"name"`
	Service    string `json:"service"`
	MetricsURL string `json:"metrics_url"`
	ReadyURL   string `json:"ready_url"`
	PID        int    `json:"pid,omitempty"`
	Manager    string `json:"manager,omitempty"`
}

type State struct {
	Active       string    `json:"active"`
	Green        Slot      `json:"green"`
	Blue         Slot      `json:"blue"`
	LastSwitchAt time.Time `json:"last_switch_at,omitempty"`
	LastResult   string    `json:"last_result,omitempty"`
	LastMessage  string    `json:"last_message,omitempty"`
	Warnings     []string  `json:"warnings,omitempty"`
}

type ActiveEndpoint struct {
	Slot       Slot
	State      State
	MetricsURL string
	ReadyURL   string
	Source     string
}

func DefaultState(cfg config.Config) State {
	cfg = cfg.WithDefaults()
	manager := "systemd"
	if runtime.GOOS == "darwin" {
		manager = "launchd"
	}
	green := Slot{
		Name:       Green,
		Service:    strings.TrimSuffix(cfg.Cloudflared.Service, ".service"),
		MetricsURL: cfg.Cloudflared.MetricsURL,
		ReadyURL:   cfg.Cloudflared.ReadyURL,
		Manager:    manager,
	}
	blueMetrics := MetricsURL(cfg.Switching.HotMetricsHost, cfg.Switching.HotMetricsPortStart, "/metrics")
	blue := Slot{
		Name:       Blue,
		Service:    DefaultBlueService(),
		MetricsURL: blueMetrics,
		ReadyURL:   strings.TrimSuffix(blueMetrics, "/metrics") + "/ready",
		Manager:    manager,
	}
	return State{Active: Green, Green: green, Blue: blue}
}

func DefaultBlueService() string {
	return BlueServiceName(runtime.GOOS)
}

func BlueServiceName(goos string) string {
	if goos == "darwin" {
		return "com.kayphoon.cfpick.cloudflared-blue"
	}
	return "cfpick-cloudflared-blue"
}

func Load(path string) (State, error) {
	var st State
	if path == "" {
		return st, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, err
	}
	return st, nil
}

func Save(path string, st State) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func LoadOrDefault(cfg config.Config) State {
	cfg = cfg.WithDefaults()
	st, err := Load(cfg.Runtime.SlotsFile)
	if err != nil {
		return DefaultState(cfg)
	}
	return st.WithDefaults(cfg)
}

func (s State) WithDefaults(cfg config.Config) State {
	def := DefaultState(cfg)
	if s.Active == "" {
		s.Active = def.Active
	}
	if s.Green.Name == "" {
		s.Green.Name = Green
	}
	if s.Green.Service == "" {
		s.Green.Service = def.Green.Service
	}
	if s.Green.MetricsURL == "" {
		s.Green.MetricsURL = def.Green.MetricsURL
	}
	if s.Green.ReadyURL == "" {
		s.Green.ReadyURL = readyFromMetrics(s.Green.MetricsURL)
	}
	if s.Green.Manager == "" {
		s.Green.Manager = def.Green.Manager
	}
	if s.Blue.Name == "" {
		s.Blue.Name = Blue
	}
	if s.Blue.Service == "" {
		s.Blue.Service = def.Blue.Service
	}
	if s.Blue.MetricsURL == "" {
		s.Blue.MetricsURL = def.Blue.MetricsURL
	}
	if s.Blue.ReadyURL == "" {
		s.Blue.ReadyURL = readyFromMetrics(s.Blue.MetricsURL)
	}
	if s.Blue.Manager == "" {
		s.Blue.Manager = def.Blue.Manager
	}
	return s
}

func (s State) ActiveSlot() Slot {
	if s.Active == Blue {
		return s.Blue
	}
	return s.Green
}

func (s State) InactiveSlot() Slot {
	if s.Active == Blue {
		return s.Green
	}
	return s.Blue
}

func (s *State) SetActive(name string) {
	if name == Blue {
		s.Active = Blue
		return
	}
	s.Active = Green
}

func MetricsURL(host string, port int, suffix string) string {
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, suffix)
}

func AddrFromMetricsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func PortFromURL(raw string) int {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	return port
}

func FindFreeMetricsAddr(host string, start, end int, exclude map[int]bool) (string, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if start <= 0 || end < start {
		return "", fmt.Errorf("invalid metrics port range %d-%d", start, end)
	}
	for port := start; port <= end; port++ {
		if exclude != nil && exclude[port] {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	return "", fmt.Errorf("no free metrics port in %s:%d-%d", host, start, end)
}

func FirstMetricsAddr(host string, start, end int, exclude map[int]bool) (string, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if start <= 0 || end < start {
		return "", fmt.Errorf("invalid metrics port range %d-%d", start, end)
	}
	for port := start; port <= end; port++ {
		if exclude != nil && exclude[port] {
			continue
		}
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	return "", fmt.Errorf("no metrics port candidate in %s:%d-%d", host, start, end)
}

func ResolveActiveEndpoint(ctx context.Context, cfg config.Config) ActiveEndpoint {
	cfg = cfg.WithDefaults()
	st := LoadOrDefault(cfg)
	candidates := []struct {
		slot   Slot
		source string
	}{
		{st.ActiveSlot(), "slots.active"},
		{st.Green, "slots.green"},
		{st.Blue, "slots.blue"},
		{Slot{Name: "config", Service: cfg.Cloudflared.Service, MetricsURL: cfg.Cloudflared.MetricsURL, ReadyURL: cfg.Cloudflared.ReadyURL}, "config"},
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if c.slot.ReadyURL == "" || seen[c.slot.ReadyURL] {
			continue
		}
		seen[c.slot.ReadyURL] = true
		ready, err := cloudflared.FetchReady(ctx, c.slot.ReadyURL)
		if err == nil && ready.ReadyConnections >= 1 {
			return ActiveEndpoint{
				Slot:       c.slot,
				State:      st,
				MetricsURL: c.slot.MetricsURL,
				ReadyURL:   c.slot.ReadyURL,
				Source:     c.source,
			}
		}
	}
	slot := st.ActiveSlot()
	if slot.MetricsURL == "" {
		slot.MetricsURL = cfg.Cloudflared.MetricsURL
	}
	if slot.ReadyURL == "" {
		slot.ReadyURL = cfg.Cloudflared.ReadyURL
	}
	return ActiveEndpoint{Slot: slot, State: st, MetricsURL: slot.MetricsURL, ReadyURL: slot.ReadyURL, Source: "fallback"}
}

func readyFromMetrics(metricsURL string) string {
	base := strings.TrimSuffix(metricsURL, "/metrics")
	if base == metricsURL {
		base = strings.TrimRight(metricsURL, "/")
	}
	return base + "/ready"
}
