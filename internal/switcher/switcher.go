package switcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kayphoon/TunnelFlux/internal/cloudflared"
	"github.com/Kayphoon/TunnelFlux/internal/config"
	"github.com/Kayphoon/TunnelFlux/internal/hosts"
	"github.com/Kayphoon/TunnelFlux/internal/service"
	"github.com/Kayphoon/TunnelFlux/internal/slots"
)

type Result struct {
	Applied       bool              `json:"applied"`
	DryRun        bool              `json:"dry_run"`
	Strategy      string            `json:"strategy"`
	Protocol      string            `json:"protocol"`
	ActiveSlot    string            `json:"active_slot,omitempty"`
	InactiveSlot  string            `json:"inactive_slot,omitempty"`
	ActiveService string            `json:"active_service,omitempty"`
	NextService   string            `json:"next_service,omitempty"`
	MetricsURL    string            `json:"metrics_url,omitempty"`
	ReadyURL      string            `json:"ready_url,omitempty"`
	HostsBackup   string            `json:"hosts_backup,omitempty"`
	ConfigBackup  string            `json:"config_backup,omitempty"`
	Ready         cloudflared.Ready `json:"ready"`
	RestartTime   time.Duration     `json:"restart_time"`
	ReadyWaitTime time.Duration     `json:"ready_wait_time"`
	Message       string            `json:"message"`
	Warnings      []string          `json:"warnings,omitempty"`
}

func Apply(ctx context.Context, cfg config.Config, ips []string, protocol string) (Result, error) {
	cfg = cfg.WithDefaults()
	if cfg.Switching.Strategy == config.SwitchStrategyRestart {
		return ApplyRestart(ctx, cfg, ips, protocol)
	}
	return ApplyHot(ctx, cfg, ips, protocol)
}

func ApplyRestart(ctx context.Context, cfg config.Config, ips []string, protocol string) (Result, error) {
	cfg = cfg.WithDefaults()
	if protocol == "" {
		protocol = cfg.Cloudflared.Protocol
	}
	mappings := hosts.Mappings(cfg.Edge.Hostnames, ips)
	if len(mappings) == 0 {
		return Result{}, fmt.Errorf("no mappings to apply")
	}
	if cfg.Runtime.DryRun {
		return Result{
			DryRun:   true,
			Strategy: config.SwitchStrategyRestart,
			Protocol: protocol,
			Message:  fmt.Sprintf("would update %s with %d mappings and restart %s", cfg.Runtime.HostsFile, len(mappings), cfg.Cloudflared.Service),
		}, nil
	}

	var res Result
	res.Strategy = config.SwitchStrategyRestart
	res.Protocol = protocol
	hostsBackup, err := hosts.Update(cfg.Runtime.HostsFile, cfg.Runtime.BackupDir, mappings)
	if err != nil {
		return res, err
	}
	res.HostsBackup = hostsBackup
	if cfg.Switching.ApplyProtocolToConfig && cfg.Cloudflared.ConfigPath != "" {
		configBackup, err := updateProtocol(cfg.Cloudflared.ConfigPath, cfg.Runtime.BackupDir, protocol)
		if err != nil {
			_ = hosts.Restore(cfg.Runtime.HostsFile, hostsBackup)
			return res, err
		}
		res.ConfigBackup = configBackup
	}

	restartCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Switching.RestartTimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now()
	if err := restartService(restartCtx, cfg); err != nil {
		_ = rollback(ctx, cfg, hostsBackup, res.ConfigBackup)
		return res, err
	}
	res.RestartTime = time.Since(start)

	waitStart := time.Now()
	ready, err := cloudflared.WaitReady(restartCtx, cfg.Cloudflared.ReadyURL, 2, 250*time.Millisecond)
	if err != nil {
		_ = rollback(ctx, cfg, hostsBackup, res.ConfigBackup)
		return res, fmt.Errorf("cloudflared did not become ready: %w", err)
	}
	res.ReadyWaitTime = time.Since(waitStart)
	res.Ready = ready
	res.Applied = true
	res.Message = "switch applied"
	return res, nil
}

func ApplyHot(ctx context.Context, cfg config.Config, ips []string, protocol string) (Result, error) {
	cfg = cfg.WithDefaults()
	if protocol == "" {
		protocol = cfg.Cloudflared.Protocol
	}
	mappings := hosts.Mappings(cfg.Edge.Hostnames, ips)
	if len(mappings) == 0 {
		return Result{}, fmt.Errorf("no mappings to apply")
	}

	mgr := service.NewManager()
	st := slots.LoadOrDefault(cfg)
	var warnings []string
	green, _, err := mgr.DiscoverGreen(ctx, cfg)
	if err != nil {
		warnings = append(warnings, "green discovery warning: "+err.Error())
		if green.Service != "" {
			st.Green = mergeSlot(st.Green, green)
		}
		if !cfg.Runtime.DryRun {
			return Result{}, fmt.Errorf("discover green slot: %w", err)
		}
	} else {
		st.Green = mergeSlot(st.Green, green)
	}
	st.Blue.Name = slots.Blue
	if st.Blue.Service == "" {
		st.Blue.Service = slots.DefaultBlueService()
	}
	st.Blue.Manager = mgr.Name()
	st.Green.Manager = mgr.Name()
	if st.Active == "" {
		st.Active = slots.Green
	}
	exclude := map[int]bool{}
	if p := slots.PortFromURL(st.Green.MetricsURL); p > 0 {
		exclude[p] = true
	}
	if p := slots.PortFromURL(st.Blue.MetricsURL); p > 0 && p != cfg.Switching.HotMetricsPortStart {
		exclude[p] = true
	}
	bluePort := slots.PortFromURL(st.Blue.MetricsURL)
	if st.Blue.MetricsURL == "" || st.Blue.ReadyURL == "" || bluePort == 0 || exclude[bluePort] {
		addr, err := slots.FindFreeMetricsAddr(cfg.Switching.HotMetricsHost, cfg.Switching.HotMetricsPortStart, cfg.Switching.HotMetricsPortEnd, exclude)
		if err != nil {
			if !cfg.Runtime.DryRun {
				return Result{}, err
			}
			portErr := err
			addr, err = slots.FirstMetricsAddr(cfg.Switching.HotMetricsHost, cfg.Switching.HotMetricsPortStart, cfg.Switching.HotMetricsPortEnd, exclude)
			if err != nil {
				return Result{}, err
			}
			warnings = append(warnings, "metrics port availability not verified: "+portErr.Error())
		}
		st.Blue.MetricsURL = "http://" + addr + "/metrics"
		st.Blue.ReadyURL = "http://" + addr + "/ready"
	}
	if oldActive, changed := reconcileActiveFromService(ctx, mgr, &st); changed {
		warnings = append(warnings, fmt.Sprintf("active slot reconciled from %s to %s using service status", oldActive, st.Active))
	}
	active := st.ActiveSlot()
	inactive := st.InactiveSlot()
	res := Result{
		DryRun:        cfg.Runtime.DryRun,
		Strategy:      config.SwitchStrategyHot,
		Protocol:      protocol,
		ActiveSlot:    active.Name,
		InactiveSlot:  inactive.Name,
		ActiveService: active.Service,
		NextService:   inactive.Service,
		MetricsURL:    inactive.MetricsURL,
		ReadyURL:      inactive.ReadyURL,
		Warnings:      warnings,
	}
	if cfg.Runtime.DryRun {
		res.Message = fmt.Sprintf("would update %s with %d mappings, start %s, wait for ready, then gracefully stop %s", cfg.Runtime.HostsFile, len(mappings), inactive.Service, active.Service)
		return res, nil
	}

	hostsBackup, err := hosts.Update(cfg.Runtime.HostsFile, cfg.Runtime.BackupDir, mappings)
	if err != nil {
		return res, err
	}
	res.HostsBackup = hostsBackup
	if cfg.Switching.ApplyProtocolToConfig && cfg.Cloudflared.ConfigPath != "" {
		configBackup, err := updateProtocol(cfg.Cloudflared.ConfigPath, cfg.Runtime.BackupDir, protocol)
		if err != nil {
			_ = hosts.Restore(cfg.Runtime.HostsFile, hostsBackup)
			return res, err
		}
		res.ConfigBackup = configBackup
	}

	if inactive.Name == slots.Blue {
		if err := mgr.InstallBlue(ctx, cfg, st.Green, st.Blue); err != nil {
			_ = rollbackHot(ctx, cfg, mgr, nil, hostsBackup, res.ConfigBackup)
			return res, err
		}
	}

	oldStatus, _ := mgr.Status(ctx, active)
	startCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Switching.HotStartTimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now()
	if err := mgr.Start(startCtx, inactive); err != nil {
		_ = rollbackHot(ctx, cfg, mgr, &inactive, hostsBackup, res.ConfigBackup)
		return res, err
	}
	res.RestartTime = time.Since(start)

	waitStart := time.Now()
	ready, err := cloudflared.WaitReady(startCtx, inactive.ReadyURL, 2, 250*time.Millisecond)
	if err != nil {
		_ = rollbackHot(ctx, cfg, mgr, &inactive, hostsBackup, res.ConfigBackup)
		return res, fmt.Errorf("inactive slot did not become ready: %w", err)
	}
	res.ReadyWaitTime = time.Since(waitStart)
	res.Ready = ready

	drainCtx, drainCancel := context.WithTimeout(ctx, time.Duration(cfg.Switching.HotDrainTimeoutSeconds)*time.Second)
	defer drainCancel()
	if err := mgr.Stop(drainCtx, active); err != nil {
		res.Warnings = append(res.Warnings, "old active stop failed: "+err.Error())
	} else if err := mgr.WaitInactive(drainCtx, active); err != nil {
		res.Warnings = append(res.Warnings, "old active inactive wait timed out: "+err.Error())
	}
	if oldStatus.PID > 0 {
		if err := mgr.WaitDrained(ctx, oldStatus.PID, cfg.Edge.Port, time.Duration(cfg.Switching.HotDrainTimeoutSeconds)*time.Second); err != nil {
			res.Warnings = append(res.Warnings, "old active edge drain timed out: "+err.Error())
		}
	}

	st.SetActive(inactive.Name)
	st.LastSwitchAt = time.Now().UTC()
	if len(res.Warnings) > 0 {
		st.LastResult = "warning"
	} else {
		st.LastResult = "success"
	}
	st.LastMessage = "hot switch applied"
	st.Warnings = res.Warnings
	_ = slots.Save(cfg.Runtime.SlotsFile, st)
	res.Applied = true
	res.Message = "hot switch applied"
	return res, nil
}

func reconcileActiveFromService(ctx context.Context, mgr service.Manager, st *slots.State) (string, bool) {
	oldActive := st.Active
	greenStatus, greenErr := mgr.Status(ctx, st.Green)
	blueStatus, blueErr := mgr.Status(ctx, st.Blue)
	greenActive := greenErr == nil && greenStatus.Active
	blueActive := blueErr == nil && blueStatus.Active
	switch {
	case greenActive && !blueActive:
		st.SetActive(slots.Green)
	case blueActive && !greenActive:
		st.SetActive(slots.Blue)
	}
	return oldActive, oldActive != st.Active
}

func mergeSlot(base, discovered slots.Slot) slots.Slot {
	if discovered.Name != "" {
		base.Name = discovered.Name
	}
	if discovered.Service != "" {
		base.Service = discovered.Service
	}
	if discovered.MetricsURL != "" {
		base.MetricsURL = discovered.MetricsURL
	}
	if discovered.ReadyURL != "" {
		base.ReadyURL = discovered.ReadyURL
	}
	if discovered.PID != 0 {
		base.PID = discovered.PID
	}
	if discovered.Manager != "" {
		base.Manager = discovered.Manager
	}
	return base
}

func rollbackHot(ctx context.Context, cfg config.Config, mgr service.Manager, started *slots.Slot, hostsBackup, configBackup string) error {
	var errs []string
	if started != nil {
		if err := mgr.Stop(ctx, *started); err != nil {
			errs = append(errs, "stop inactive slot: "+err.Error())
		}
	}
	if hostsBackup != "" {
		if err := hosts.Restore(cfg.Runtime.HostsFile, hostsBackup); err != nil {
			errs = append(errs, "restore hosts: "+err.Error())
		}
	}
	if configBackup != "" {
		if err := restoreFile(cfg.Cloudflared.ConfigPath, configBackup); err != nil {
			errs = append(errs, "restore cloudflared config: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func restartService(ctx context.Context, cfg config.Config) error {
	systemctl := cfg.Cloudflared.Systemctl
	if systemctl == "" {
		systemctl = "systemctl"
	}
	service := cfg.Cloudflared.Service
	if service == "" {
		service = "cloudflared"
	}
	cmd := exec.CommandContext(ctx, systemctl, "restart", service)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart %s failed: %w: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func rollback(ctx context.Context, cfg config.Config, hostsBackup, configBackup string) error {
	var errs []string
	if hostsBackup != "" {
		if err := hosts.Restore(cfg.Runtime.HostsFile, hostsBackup); err != nil {
			errs = append(errs, "restore hosts: "+err.Error())
		}
	}
	if configBackup != "" {
		if err := restoreFile(cfg.Cloudflared.ConfigPath, configBackup); err != nil {
			errs = append(errs, "restore cloudflared config: "+err.Error())
		}
	}
	if err := restartService(ctx, cfg); err != nil {
		errs = append(errs, "restart after rollback: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func updateProtocol(path, backupDir, protocol string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if backupDir == "" {
		backupDir = filepath.Dir(path)
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}
	backup := filepath.Join(backupDir, fmt.Sprintf("%s.bak-%s", filepath.Base(path), time.Now().UTC().Format("20060102150405")))
	if err := os.WriteFile(backup, data, 0644); err != nil {
		return "", err
	}

	re := regexp.MustCompile(`^\s*protocol\s*:`)
	var lines []string
	found := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Text()
		if re.MatchString(line) {
			lines = append(lines, "protocol: "+protocol)
			found = true
		} else {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return backup, err
	}
	if !found {
		lines = append([]string{"protocol: " + protocol}, lines...)
	}
	next := strings.Join(lines, "\n")
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	tmp := path + ".tunnelflux.tmp"
	if err := os.WriteFile(tmp, []byte(next), 0644); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return backup, err
	}
	return backup, nil
}

func restoreFile(path, backup string) error {
	data, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	tmp := path + ".tunnelflux.restore.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
