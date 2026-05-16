package cloudflared

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Metrics struct {
	HAConnections      int
	ConcurrentRequests int
	TotalRequests      float64
	RequestErrors      float64
	ServerLocations    map[string]string
	Raw                map[string]float64
}

type Ready struct {
	Status           int    `json:"status"`
	ReadyConnections int    `json:"readyConnections"`
	ConnectorID      string `json:"connectorId"`
}

type EdgeConnection struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
	IP     string `json:"ip"`
	Line   string `json:"line"`
}

func FetchMetrics(ctx context.Context, url string) (Metrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Metrics{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Metrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Metrics{}, fmt.Errorf("metrics endpoint returned %s", resp.Status)
	}
	return ParseMetrics(resp.Body)
}

func ParseMetrics(r io.Reader) (Metrics, error) {
	m := Metrics{Raw: map[string]float64{}, ServerLocations: map[string]string{}}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := splitMetric(line)
		if !ok {
			continue
		}
		m.Raw[name] = value
		switch {
		case name == "cloudflared_tunnel_ha_connections":
			m.HAConnections = int(value)
		case name == "cloudflared_tunnel_concurrent_requests_per_tunnel":
			m.ConcurrentRequests = int(value)
		case name == "cloudflared_tunnel_total_requests":
			m.TotalRequests = value
		case name == "cloudflared_tunnel_request_errors":
			m.RequestErrors = value
		case strings.HasPrefix(name, "cloudflared_tunnel_server_locations"):
			if value == 1 {
				conn := labelValue(name, "connection_id")
				loc := labelValue(name, "edge_location")
				if conn != "" && loc != "" {
					m.ServerLocations[conn] = loc
				}
			}
		}
	}
	return m, sc.Err()
}

func splitMetric(line string) (string, float64, bool) {
	idx := strings.LastIndexAny(line, " \t")
	if idx < 0 {
		return "", 0, false
	}
	name := strings.TrimSpace(line[:idx])
	raw := strings.TrimSpace(line[idx+1:])
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", 0, false
	}
	return name, value, true
}

func labelValue(metric, key string) string {
	start := strings.IndexByte(metric, '{')
	end := strings.LastIndexByte(metric, '}')
	if start < 0 || end <= start {
		return ""
	}
	labels := metric[start+1 : end]
	for _, part := range strings.Split(labels, ",") {
		part = strings.TrimSpace(part)
		prefix := key + "=\""
		if strings.HasPrefix(part, prefix) && strings.HasSuffix(part, "\"") {
			return strings.TrimSuffix(strings.TrimPrefix(part, prefix), "\"")
		}
	}
	return ""
}

func FetchReady(ctx context.Context, url string) (Ready, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Ready{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Ready{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Ready{}, fmt.Errorf("ready endpoint returned %s", resp.Status)
	}
	var ready Ready
	if err := json.NewDecoder(resp.Body).Decode(&ready); err != nil {
		return Ready{}, err
	}
	return ready, nil
}

func CurrentEdges(ctx context.Context, port int) ([]EdgeConnection, error) {
	cmd := exec.CommandContext(ctx, "ss", "-ntp")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var conns []EdgeConnection
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "cloudflared") || !strings.Contains(line, fmt.Sprintf(":%d", port)) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		remote := fields[4]
		ip := hostPart(remote)
		conns = append(conns, EdgeConnection{
			Local:  fields[3],
			Remote: remote,
			IP:     ip,
			Line:   line,
		})
	}
	return conns, nil
}

func hostPart(addr string) string {
	addr = strings.Trim(addr, "[]")
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return strings.Trim(addr[:i], "[]")
	}
	return addr
}

func WaitReady(ctx context.Context, readyURL string, want int, interval time.Duration) (Ready, error) {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		ready, err := FetchReady(ctx, readyURL)
		if err == nil && ready.ReadyConnections >= want {
			return ready, nil
		}
		select {
		case <-ctx.Done():
			return Ready{}, ctx.Err()
		case <-t.C:
		}
	}
}
