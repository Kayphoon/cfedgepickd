package cloudflared

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Metrics struct {
	HAConnections           int
	ConcurrentRequests      int
	TotalRequests           float64
	RequestErrors           float64
	ResponseByCode          map[string]float64
	Response2xx             float64
	Response3xx             float64
	Response4xx             float64
	Response5xx             float64
	ProxyConnectLatencySum  float64
	ProxyConnectLatencyHits float64
	TCPActiveSessions       float64
	TCPTotalSessions        float64
	UDPActiveSessions       float64
	UDPTotalSessions        float64
	ProcessCPUSeconds       float64
	ProcessRSSBytes         float64
	ProcessNetworkRxBytes   float64
	ProcessNetworkTxBytes   float64
	GoGoroutines            float64
	GoThreads               float64
	GoHeapAllocBytes        float64
	ServerLocations         map[string]string
	Raw                     map[string]float64
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
	Source string `json:"source,omitempty"`
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
	m := Metrics{Raw: map[string]float64{}, ResponseByCode: map[string]float64{}, ServerLocations: map[string]string{}}
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
		base := metricBase(name)
		switch {
		case base == "cloudflared_tunnel_ha_connections":
			m.HAConnections = int(value)
		case base == "cloudflared_tunnel_concurrent_requests_per_tunnel":
			m.ConcurrentRequests = int(value)
		case base == "cloudflared_tunnel_total_requests":
			m.TotalRequests = value
		case base == "cloudflared_tunnel_request_errors":
			m.RequestErrors = value
		case base == "cloudflared_tunnel_response_by_code":
			code := labelValue(name, "status_code")
			if code != "" {
				m.ResponseByCode[code] = value
				switch {
				case strings.HasPrefix(code, "2"):
					m.Response2xx += value
				case strings.HasPrefix(code, "3"):
					m.Response3xx += value
				case strings.HasPrefix(code, "4"):
					m.Response4xx += value
				case strings.HasPrefix(code, "5"):
					m.Response5xx += value
				}
			}
		case base == "cloudflared_proxy_connect_latency_sum":
			m.ProxyConnectLatencySum = value
		case base == "cloudflared_proxy_connect_latency_count":
			m.ProxyConnectLatencyHits = value
		case base == "cloudflared_tcp_active_sessions":
			m.TCPActiveSessions = value
		case base == "cloudflared_tcp_total_sessions":
			m.TCPTotalSessions = value
		case base == "cloudflared_udp_active_sessions":
			m.UDPActiveSessions = value
		case base == "cloudflared_udp_total_sessions":
			m.UDPTotalSessions = value
		case base == "process_cpu_seconds_total":
			m.ProcessCPUSeconds = value
		case base == "process_resident_memory_bytes":
			m.ProcessRSSBytes = value
		case base == "process_network_receive_bytes_total":
			m.ProcessNetworkRxBytes = value
		case base == "process_network_transmit_bytes_total":
			m.ProcessNetworkTxBytes = value
		case base == "go_goroutines":
			m.GoGoroutines = value
		case base == "go_threads":
			m.GoThreads = value
		case base == "go_memstats_heap_alloc_bytes":
			m.GoHeapAllocBytes = value
		case base == "cloudflared_tunnel_server_locations":
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

func metricBase(name string) string {
	if i := strings.IndexByte(name, '{'); i >= 0 {
		return name[:i]
	}
	return name
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
	if runtime.GOOS == "darwin" {
		return currentEdgesLsof(ctx, port)
	}
	cmd := exec.CommandContext(ctx, "ss", "-H", "-ntup")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseSSConnections(string(out), port), nil
}

func parseSSConnections(output string, port int) []EdgeConnection {
	portNeedle := fmt.Sprintf(":%d", port)
	var configuredPort []EdgeConnection
	var discovered []EdgeConnection
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		local, remote, ok := ssLocalRemote(fields, portNeedle)
		if ok {
			if strings.Contains(line, "users:") && !strings.Contains(line, "cloudflared") {
				continue
			}
			configuredPort = append(configuredPort, EdgeConnection{
				Local:  local,
				Remote: remote,
				IP:     hostPart(remote),
				Line:   line,
				Source: "socket",
			})
			continue
		}

		if !strings.Contains(line, "cloudflared") {
			continue
		}
		local, remote, ok = ssPeer(fields)
		if !ok {
			continue
		}
		if isPublicRemote(remote) {
			discovered = append(discovered, EdgeConnection{
				Local:  local,
				Remote: remote,
				IP:     hostPart(remote),
				Line:   line,
				Source: "socket",
			})
		}
	}
	if len(configuredPort) > 0 {
		return configuredPort
	}
	return discovered
}

func ssLocalRemote(fields []string, portNeedle string) (string, string, bool) {
	for i, field := range fields {
		if strings.Contains(field, portNeedle) {
			if i == 0 {
				return "", "", false
			}
			return fields[i-1], field, true
		}
	}
	return "", "", false
}

func ssPeer(fields []string) (string, string, bool) {
	if len(fields) >= 6 && (fields[0] == "tcp" || fields[0] == "udp") {
		return fields[4], fields[5], true
	}
	if len(fields) >= 5 {
		return fields[3], fields[4], true
	}
	return "", "", false
}

func isPublicRemote(remote string) bool {
	ip := hostPart(remote)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()
}

func currentEdgesLsof(ctx context.Context, port int) ([]EdgeConnection, error) {
	portFilter := strconv.Itoa(port)
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+portFilter, "-iUDP:"+portFilter)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var conns []EdgeConnection
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "cloudflared") || !strings.Contains(line, "->") || !strings.Contains(line, fmt.Sprintf(":%d", port)) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[len(fields)-1]
		parts := strings.Split(name, "->")
		if len(parts) != 2 {
			continue
		}
		remote := strings.TrimSpace(strings.TrimSuffix(parts[1], "(ESTABLISHED)"))
		conns = append(conns, EdgeConnection{
			Local:  strings.TrimSpace(parts[0]),
			Remote: remote,
			IP:     hostPart(remote),
			Line:   line,
			Source: "socket",
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
