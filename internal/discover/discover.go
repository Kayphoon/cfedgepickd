package discover

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kayphoon/cfpick/internal/config"
)

type Report struct {
	Config        config.Config `json:"config"`
	Notes         []string      `json:"notes"`
	Warnings      []string      `json:"warnings"`
	MetricsFound  bool          `json:"metrics_found"`
	CandidateFile bool          `json:"candidate_file_found"`
}

func Run(ctx context.Context) (Report, error) {
	cfg := config.Default()
	rep := Report{Config: cfg}

	if path, err := exec.LookPath("systemctl"); err == nil {
		cfg.Cloudflared.Systemctl = path
	} else {
		rep.Warnings = append(rep.Warnings, "systemctl not found; only systemd Linux is supported in v1")
	}
	if path, err := exec.LookPath("cloudflared"); err == nil {
		cfg.Cloudflared.Binary = path
	} else if _, err := os.Stat("/usr/local/bin/cloudflared"); err == nil {
		cfg.Cloudflared.Binary = "/usr/local/bin/cloudflared"
	}

	service, err := findCloudflaredService(ctx, cfg.Cloudflared.Systemctl)
	if err == nil && service != "" {
		cfg.Cloudflared.Service = service
		rep.Notes = append(rep.Notes, "found cloudflared service: "+service)
	} else if err != nil {
		rep.Warnings = append(rep.Warnings, "cloudflared service discovery failed: "+err.Error())
	}

	if unit, err := systemctlCat(ctx, cfg.Cloudflared.Systemctl, cfg.Cloudflared.Service); err == nil {
		if bin := parseExecBinary(unit); bin != "" {
			cfg.Cloudflared.Binary = bin
		}
		if p := parseConfigPath(unit); p != "" {
			cfg.Cloudflared.ConfigPath = p
		}
		if m := parseMetricsArg(unit); m != "" {
			cfg.Cloudflared.MetricsURL = normalizeMetricsURL(m, "/metrics")
			cfg.Cloudflared.ReadyURL = normalizeMetricsURL(m, "/ready")
		}
	} else {
		rep.Warnings = append(rep.Warnings, "systemctl cat failed: "+err.Error())
	}

	if protocol, err := parseProtocol(cfg.Cloudflared.ConfigPath); err == nil && protocol != "" {
		cfg.Cloudflared.Protocol = protocol
		rep.Notes = append(rep.Notes, "found protocol in cloudflared config: "+protocol)
	}

	if !probeHTTP(ctx, cfg.Cloudflared.MetricsURL) {
		if metricsURL, ok := scanMetrics(ctx); ok {
			cfg.Cloudflared.MetricsURL = metricsURL + "/metrics"
			cfg.Cloudflared.ReadyURL = metricsURL + "/ready"
			rep.MetricsFound = true
		} else {
			rep.Warnings = append(rep.Warnings, "metrics endpoint not found on 127.0.0.1:20241-20245")
		}
	} else {
		rep.MetricsFound = true
	}

	if _, err := os.Stat(cfg.Edge.CandidateFile); err == nil {
		rep.CandidateFile = true
	} else {
		rep.Warnings = append(rep.Warnings, "candidate_file not found; using built-in default Cloudflare edge CIDRs")
		cfg.Edge.CandidateFile = ""
	}

	cfg = cfg.WithDefaults()
	rep.Config = cfg
	return rep, nil
}

func findCloudflaredService(ctx context.Context, systemctl string) (string, error) {
	if systemctl == "" {
		return "", errors.New("systemctl path is empty")
	}
	out, err := exec.CommandContext(ctx, systemctl, "list-units", "--all", "--type=service", "--no-legend").Output()
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if strings.Contains(fields[0], "cloudflared") {
			return strings.TrimSpace(fields[0]), nil
		}
	}
	return "cloudflared.service", nil
}

func systemctlCat(ctx context.Context, systemctl, service string) (string, error) {
	out, err := exec.CommandContext(ctx, systemctl, "cat", service).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseExecBinary(unit string) string {
	re := regexp.MustCompile(`(?m)^ExecStart=([^ \t]+)`)
	m := re.FindStringSubmatch(unit)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func parseConfigPath(unit string) string {
	re := regexp.MustCompile(`--config[= ]([^ \t]+)`)
	m := re.FindStringSubmatch(unit)
	if len(m) == 2 {
		return strings.Trim(m[1], `"'`)
	}
	return ""
}

func parseMetricsArg(unit string) string {
	re := regexp.MustCompile(`--metrics[= ]([^ \t]+)`)
	m := re.FindStringSubmatch(unit)
	if len(m) == 2 {
		return strings.Trim(m[1], `"'`)
	}
	return ""
}

func parseProtocol(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`(?m)^\s*protocol\s*:\s*([A-Za-z0-9_-]+)\s*$`)
	m := re.FindSubmatch(data)
	if len(m) == 2 {
		p := strings.ToLower(string(m[1]))
		switch p {
		case config.ProtocolAuto, config.ProtocolQUIC, config.ProtocolHTTP2:
			return p, nil
		}
		return p, fmt.Errorf("unknown protocol %q", p)
	}
	return "", nil
}

func normalizeMetricsURL(addr, suffix string) string {
	addr = strings.TrimSpace(addr)
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + suffix
}

func probeHTTP(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func scanMetrics(ctx context.Context) (string, bool) {
	for port := 20241; port <= 20245; port++ {
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		if probeHTTP(ctx, base+"/metrics") && probeHTTP(ctx, base+"/ready") {
			return base, true
		}
	}
	return "", false
}
