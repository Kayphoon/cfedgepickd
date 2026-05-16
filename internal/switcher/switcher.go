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

	"github.com/kayphoon/cfedgepickd/internal/cloudflared"
	"github.com/kayphoon/cfedgepickd/internal/config"
	"github.com/kayphoon/cfedgepickd/internal/hosts"
)

type Result struct {
	Applied       bool              `json:"applied"`
	DryRun        bool              `json:"dry_run"`
	Protocol      string            `json:"protocol"`
	HostsBackup   string            `json:"hosts_backup,omitempty"`
	ConfigBackup  string            `json:"config_backup,omitempty"`
	Ready         cloudflared.Ready `json:"ready"`
	RestartTime   time.Duration     `json:"restart_time"`
	ReadyWaitTime time.Duration     `json:"ready_wait_time"`
	Message       string            `json:"message"`
}

func Apply(ctx context.Context, cfg config.Config, ips []string, protocol string) (Result, error) {
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
			Protocol: protocol,
			Message:  fmt.Sprintf("would update %s with %d mappings and restart %s", cfg.Runtime.HostsFile, len(mappings), cfg.Cloudflared.Service),
		}, nil
	}

	var res Result
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
	tmp := path + ".cfpick.tmp"
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
	tmp := path + ".cfpick.restore.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
