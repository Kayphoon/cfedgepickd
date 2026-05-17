package service

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kayphoon/tunnelflux/internal/config"
	"github.com/kayphoon/tunnelflux/internal/slots"
)

type SlotStatus struct {
	Active bool `json:"active"`
	PID    int  `json:"pid,omitempty"`
}

type Manager interface {
	Name() string
	DiscoverGreen(ctx context.Context, cfg config.Config) (slots.Slot, []string, error)
	InstallBlue(ctx context.Context, cfg config.Config, green slots.Slot, blue slots.Slot) error
	Start(ctx context.Context, slot slots.Slot) error
	Stop(ctx context.Context, slot slots.Slot) error
	Status(ctx context.Context, slot slots.Slot) (SlotStatus, error)
	WaitInactive(ctx context.Context, slot slots.Slot) error
	WaitDrained(ctx context.Context, pid int, port int, timeout time.Duration) error
}

func NewManager() Manager {
	if runtime.GOOS == "darwin" {
		return LaunchdManager{}
	}
	return SystemdManager{}
}

func NormalizeServiceName(service string) string {
	return strings.TrimSuffix(strings.TrimSpace(service), ".service")
}

func EnsureMetricsArg(args []string, metricsAddr string) []string {
	if metricsAddr == "" {
		return append([]string(nil), args...)
	}
	out := make([]string, 0, len(args)+2)
	inserted := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--metrics" {
			out = append(out, "--metrics", metricsAddr)
			i++
			inserted = true
			continue
		}
		if strings.HasPrefix(arg, "--metrics=") {
			out = append(out, "--metrics="+metricsAddr)
			inserted = true
			continue
		}
		if !inserted && arg == "tunnel" {
			out = append(out, "--metrics", metricsAddr)
			inserted = true
		}
		out = append(out, arg)
	}
	if !inserted {
		if len(out) == 0 {
			return []string{"--metrics", metricsAddr}
		}
		out = append(out[:1], append([]string{"--metrics", metricsAddr}, out[1:]...)...)
	}
	return out
}

func FallbackCloudflaredArgs(cfg config.Config, metricsAddr string) []string {
	cfg = cfg.WithDefaults()
	args := []string{cfg.Cloudflared.Binary, "--config", cfg.Cloudflared.ConfigPath, "tunnel", "run"}
	return EnsureMetricsArg(args, metricsAddr)
}

func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == '@' || r == '+' || r == ',' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func RenderSystemdUnit(serviceName string, args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, ShellQuote(arg))
	}
	lines := []string{
		"[Unit]",
		"Description=TunnelFlux blue cloudflared slot",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"User=root",
		"ExecStart=" + strings.Join(quoted, " "),
		"Restart=on-failure",
		"RestartSec=5s",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}
	return strings.Join(lines, "\n")
}

func RenderLaunchdPlist(label string, args []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	writePlistKeyString(&b, "Label", label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range args {
		b.WriteString("\t\t<string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	writePlistKeyString(&b, "StandardOutPath", "/var/log/tunnelflux-cloudflared-blue.log")
	writePlistKeyString(&b, "StandardErrorPath", "/var/log/tunnelflux-cloudflared-blue.err.log")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func writePlistKeyString(b *strings.Builder, key, value string) {
	b.WriteString("\t<key>" + xmlEscape(key) + "</key>\n")
	b.WriteString("\t<string>" + xmlEscape(value) + "</string>\n")
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

type SystemdManager struct{}

func (SystemdManager) Name() string { return "systemd" }

func (SystemdManager) DiscoverGreen(ctx context.Context, cfg config.Config) (slots.Slot, []string, error) {
	cfg = cfg.WithDefaults()
	serviceName := NormalizeServiceName(cfg.Cloudflared.Service)
	slot := slots.Slot{
		Name:       slots.Green,
		Service:    serviceName,
		MetricsURL: cfg.Cloudflared.MetricsURL,
		ReadyURL:   cfg.Cloudflared.ReadyURL,
		Manager:    "systemd",
	}
	out, err := exec.CommandContext(ctx, cfg.Cloudflared.Systemctl, "cat", serviceName+".service").Output()
	if err != nil {
		return slot, FallbackCloudflaredArgs(cfg, slots.AddrFromMetricsURL(cfg.Cloudflared.MetricsURL)), err
	}
	args := ParseSystemdExecStart(string(out))
	if len(args) == 0 {
		args = FallbackCloudflaredArgs(cfg, slots.AddrFromMetricsURL(cfg.Cloudflared.MetricsURL))
	}
	if m := MetricsArg(args); m != "" {
		slot.MetricsURL = slots.MetricsURL(hostOrDefault(m, cfg.Switching.HotMetricsHost), portOrDefault(m, cfg.Switching.HotMetricsPortStart), "/metrics")
		slot.ReadyURL = strings.TrimSuffix(slot.MetricsURL, "/metrics") + "/ready"
	}
	return slot, args, nil
}

func (SystemdManager) InstallBlue(ctx context.Context, cfg config.Config, green slots.Slot, blue slots.Slot) error {
	cfg = cfg.WithDefaults()
	_, greenArgs, _ := (SystemdManager{}).DiscoverGreen(ctx, cfg)
	args := EnsureMetricsArg(greenArgs, slots.AddrFromMetricsURL(blue.MetricsURL))
	unitPath := filepath.Join("/etc/systemd/system", NormalizeServiceName(blue.Service)+".service")
	if err := os.WriteFile(unitPath, []byte(RenderSystemdUnit(blue.Service, args)), 0644); err != nil {
		return err
	}
	_ = green
	return exec.CommandContext(ctx, cfg.Cloudflared.Systemctl, "daemon-reload").Run()
}

func (SystemdManager) Start(ctx context.Context, slot slots.Slot) error {
	return runCombined(ctx, "systemctl", "start", NormalizeServiceName(slot.Service)+".service")
}

func (SystemdManager) Stop(ctx context.Context, slot slots.Slot) error {
	return runCombined(ctx, "systemctl", "stop", NormalizeServiceName(slot.Service)+".service")
}

func (SystemdManager) Status(ctx context.Context, slot slots.Slot) (SlotStatus, error) {
	name := NormalizeServiceName(slot.Service) + ".service"
	out, err := exec.CommandContext(ctx, "systemctl", "show", name, "-p", "ActiveState", "-p", "MainPID", "--value").Output()
	if err != nil {
		return SlotStatus{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	st := SlotStatus{}
	if len(lines) > 0 {
		st.Active = strings.TrimSpace(lines[0]) == "active"
	}
	if len(lines) > 1 {
		st.PID, _ = strconv.Atoi(strings.TrimSpace(lines[1]))
	}
	return st, nil
}

func (m SystemdManager) WaitInactive(ctx context.Context, slot slots.Slot) error {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		st, err := m.Status(ctx, slot)
		if err == nil && !st.Active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (SystemdManager) WaitDrained(ctx context.Context, pid int, port int, timeout time.Duration) error {
	return WaitPIDDrained(ctx, pid, port, timeout)
}

func ParseSystemdExecStart(unit string) []string {
	sc := bufio.NewScanner(strings.NewReader(unit))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "ExecStart=") {
			return splitExec(strings.TrimPrefix(line, "ExecStart="))
		}
	}
	return nil
}

func splitExec(raw string) []string {
	var args []string
	var b strings.Builder
	inQuote := rune(0)
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			inQuote = r
			continue
		}
		if r == ' ' || r == '\t' {
			if b.Len() > 0 {
				args = append(args, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		args = append(args, b.String())
	}
	return args
}

type LaunchdManager struct{}

func (LaunchdManager) Name() string { return "launchd" }

func (LaunchdManager) DiscoverGreen(ctx context.Context, cfg config.Config) (slots.Slot, []string, error) {
	cfg = cfg.WithDefaults()
	label := cfg.Cloudflared.Service
	if label == "" || label == "cloudflared" {
		var err error
		label, err = discoverLaunchdLabel(ctx)
		if err != nil {
			return slots.Slot{
				Name:       slots.Green,
				Service:    "com.cloudflare.cloudflared",
				MetricsURL: cfg.Cloudflared.MetricsURL,
				ReadyURL:   cfg.Cloudflared.ReadyURL,
				Manager:    "launchd",
			}, FallbackCloudflaredArgs(cfg, slots.AddrFromMetricsURL(cfg.Cloudflared.MetricsURL)), err
		}
	}
	slot := slots.Slot{
		Name:       slots.Green,
		Service:    label,
		MetricsURL: cfg.Cloudflared.MetricsURL,
		ReadyURL:   cfg.Cloudflared.ReadyURL,
		Manager:    "launchd",
	}
	args := FallbackCloudflaredArgs(cfg, slots.AddrFromMetricsURL(cfg.Cloudflared.MetricsURL))
	if path := launchdPlistPath(label); path != "" {
		if parsed, err := ParseLaunchdProgramArguments(path); err == nil && len(parsed) > 0 {
			args = parsed
			if m := MetricsArg(args); m != "" {
				slot.MetricsURL = slots.MetricsURL(hostOrDefault(m, cfg.Switching.HotMetricsHost), portOrDefault(m, cfg.Switching.HotMetricsPortStart), "/metrics")
				slot.ReadyURL = strings.TrimSuffix(slot.MetricsURL, "/metrics") + "/ready"
			}
		}
		return slot, args, nil
	}
	if exec.CommandContext(ctx, "launchctl", "print", "system/"+label).Run() == nil {
		return slot, args, nil
	}
	return slot, args, fmt.Errorf("cloudflared launchd service %q not found", label)
}

func (LaunchdManager) InstallBlue(ctx context.Context, cfg config.Config, green slots.Slot, blue slots.Slot) error {
	cfg = cfg.WithDefaults()
	_, greenArgs, _ := (LaunchdManager{}).DiscoverGreen(ctx, cfg)
	args := EnsureMetricsArg(greenArgs, slots.AddrFromMetricsURL(blue.MetricsURL))
	path := launchdPlistPath(blue.Service)
	if path == "" {
		path = "/Library/LaunchDaemons/" + blue.Service + ".plist"
	}
	_ = green
	return os.WriteFile(path, []byte(RenderLaunchdPlist(blue.Service, args)), 0644)
}

func (LaunchdManager) Start(ctx context.Context, slot slots.Slot) error {
	path := launchdPlistPath(slot.Service)
	if path != "" {
		_ = exec.CommandContext(ctx, "launchctl", "bootstrap", "system", path).Run()
	}
	return runCombined(ctx, "launchctl", "kickstart", "-k", "system/"+slot.Service)
}

func (LaunchdManager) Stop(ctx context.Context, slot slots.Slot) error {
	if err := runCombined(ctx, "launchctl", "bootout", "system/"+slot.Service); err == nil {
		return nil
	}
	return runCombined(ctx, "launchctl", "stop", slot.Service)
}

func (LaunchdManager) Status(ctx context.Context, slot slots.Slot) (SlotStatus, error) {
	out, err := exec.CommandContext(ctx, "launchctl", "print", "system/"+slot.Service).Output()
	if err != nil {
		return SlotStatus{}, err
	}
	st := SlotStatus{Active: true}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pid = ") {
			st.PID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		}
		if strings.Contains(line, "state = not running") {
			st.Active = false
		}
	}
	return st, nil
}

func (m LaunchdManager) WaitInactive(ctx context.Context, slot slots.Slot) error {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		st, err := m.Status(ctx, slot)
		if err != nil || !st.Active || st.PID == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (LaunchdManager) WaitDrained(ctx context.Context, pid int, port int, timeout time.Duration) error {
	return WaitPIDDrained(ctx, pid, port, timeout)
}

func ParseLaunchdProgramArguments(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var inProgramArgs bool
	var inString bool
	var lastKey string
	var args []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "key" || (inProgramArgs && t.Name.Local == "string") {
				inString = t.Name.Local == "string"
			}
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			if inProgramArgs && inString {
				args = append(args, text)
			} else {
				lastKey = text
			}
		case xml.EndElement:
			if t.Name.Local == "key" && lastKey == "ProgramArguments" {
				inProgramArgs = true
			}
			if t.Name.Local == "array" && inProgramArgs {
				return args, nil
			}
			if t.Name.Local == "string" {
				inString = false
			}
		}
	}
	if len(args) == 0 {
		return nil, errors.New("ProgramArguments not found")
	}
	return args, nil
}

func MetricsArg(args []string) string {
	for i, arg := range args {
		if arg == "--metrics" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--metrics=") {
			return strings.TrimPrefix(arg, "--metrics=")
		}
	}
	return ""
}

func WaitPIDDrained(ctx context.Context, pid int, port int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	drainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		has, err := PIDHasPortConnection(drainCtx, pid, port)
		if err != nil || !has {
			return nil
		}
		select {
		case <-drainCtx.Done():
			return drainCtx.Err()
		case <-t.C:
		}
	}
}

func PIDHasPortConnection(ctx context.Context, pid int, port int) (bool, error) {
	if runtime.GOOS == "darwin" {
		out, err := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP:"+strconv.Itoa(port)).Output()
		return strings.TrimSpace(string(out)) != "", err
	}
	out, err := exec.CommandContext(ctx, "ss", "-ntp").Output()
	if err != nil {
		return false, err
	}
	needle := fmt.Sprintf("pid=%d,", pid)
	portNeedle := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, needle) && strings.Contains(line, portNeedle) {
			return true, nil
		}
	}
	return false, nil
}

func runCombined(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func discoverLaunchdLabel(ctx context.Context) (string, error) {
	candidates := []string{"com.cloudflare.cloudflared", "cloudflared"}
	for _, label := range candidates {
		if exec.CommandContext(ctx, "launchctl", "print", "system/"+label).Run() == nil {
			return label, nil
		}
	}
	matches, _ := filepath.Glob("/Library/LaunchDaemons/*.plist")
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(strings.ToLower(string(data)), "cloudflared") {
			base := filepath.Base(path)
			return strings.TrimSuffix(base, ".plist"), nil
		}
	}
	return "", errors.New("cloudflared launchd service not found")
}

func launchdPlistPath(label string) string {
	if label == "" {
		return ""
	}
	if strings.HasPrefix(label, "/") {
		return label
	}
	return "/Library/LaunchDaemons/" + label + ".plist"
}

func hostOrDefault(addr, fallback string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return fallback
	}
	return host
}

func portOrDefault(addr string, fallback int) int {
	_, raw, err := net.SplitHostPort(addr)
	if err != nil {
		return fallback
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return port
}
