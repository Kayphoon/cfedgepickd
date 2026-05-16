package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/kayphoon/cfedgepickd/internal/config"
)

type Mode string

const (
	ModeAuto  Mode = config.ProtocolAuto
	ModeQUIC  Mode = config.ProtocolQUIC
	ModeHTTP2 Mode = config.ProtocolHTTP2
)

type Result struct {
	IP       string    `json:"ip"`
	Protocol string    `json:"protocol"`
	OK       int       `json:"ok"`
	Fail     int       `json:"fail"`
	MedianMS float64   `json:"median_ms"`
	MeanMS   float64   `json:"mean_ms"`
	MinMS    float64   `json:"min_ms"`
	MaxMS    float64   `json:"max_ms"`
	StdevMS  float64   `json:"stdev_ms"`
	Score    float64   `json:"score"`
	Errors   []string  `json:"errors,omitempty"`
	When     time.Time `json:"when"`
}

type Report struct {
	Mode              string   `json:"mode"`
	EffectiveProtocol string   `json:"effective_protocol"`
	Candidates        int      `json:"candidates"`
	Results           []Result `json:"results"`
	Top               []Result `json:"top"`
}

func Run(ctx context.Context, cfg config.Config, mode Mode) (Report, error) {
	cfg = cfg.WithDefaults()
	if mode == "" {
		mode = Mode(cfg.Cloudflared.Protocol)
	}
	if mode == ModeAuto || mode == ModeQUIC {
		rep, err := runMode(ctx, cfg, ModeQUIC)
		if err == nil && len(rep.Top) >= minTop(cfg.Edge.TopN, len(cfg.Edge.Hostnames)) {
			rep.Mode = string(mode)
			rep.EffectiveProtocol = config.ProtocolQUIC
			return rep, nil
		}
		if mode == ModeQUIC {
			if err != nil {
				return rep, err
			}
			return rep, errors.New("quic probe did not produce enough healthy candidates")
		}
	}
	rep, err := runMode(ctx, cfg, ModeHTTP2)
	rep.Mode = string(mode)
	rep.EffectiveProtocol = config.ProtocolHTTP2
	return rep, err
}

func runMode(ctx context.Context, cfg config.Config, mode Mode) (Report, error) {
	items, err := candidates(cfg)
	if err != nil {
		return Report{}, err
	}
	if len(items) == 0 {
		return Report{}, errors.New("no candidate IPs")
	}
	timeout, err := time.ParseDuration(cfg.Edge.ProbeTimeout)
	if err != nil {
		return Report{}, err
	}
	rounds := cfg.Edge.ProbeRounds
	if rounds <= 0 {
		rounds = 5
	}
	concurrency := cfg.Edge.Concurrency
	if concurrency <= 0 {
		concurrency = 64
	}

	jobs := make(chan string)
	results := make(chan Result, len(items))
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				results <- probeIP(ctx, ip, cfg.Edge.Port, cfg.Edge.ServerName, mode, rounds, timeout)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ip := range items {
			select {
			case <-ctx.Done():
				return
			case jobs <- ip:
			}
		}
	}()
	wg.Wait()
	close(results)

	var rows []Result
	for row := range results {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return better(rows[i], rows[j])
	})
	topN := cfg.Edge.TopN
	if topN <= 0 {
		topN = 4
	}
	top := uniqueTop(rows, topN)
	return Report{
		Mode:              string(mode),
		EffectiveProtocol: string(mode),
		Candidates:        len(items),
		Results:           rows,
		Top:               top,
	}, nil
}

func candidates(cfg config.Config) ([]string, error) {
	var raw []string
	if cfg.Edge.CandidateFile != "" {
		if data, err := os.ReadFile(cfg.Edge.CandidateFile); err == nil {
			raw = append(raw, strings.Fields(string(data))...)
		}
	}
	raw = append(raw, cfg.Edge.Candidates...)
	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" || strings.HasPrefix(item, "#") {
			continue
		}
		var expanded []string
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				continue
			}
			expanded = expandPrefix(p, cfg.Edge.MaxCandidates)
		} else if addr, err := netip.ParseAddr(item); err == nil {
			expanded = []string{addr.String()}
		}
		for _, ip := range expanded {
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
				if cfg.Edge.MaxCandidates > 0 && len(out) >= cfg.Edge.MaxCandidates {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

func expandPrefix(p netip.Prefix, max int) []string {
	p = p.Masked()
	var out []string
	addr := p.Addr()
	for p.Contains(addr) {
		if addr.Is4() {
			last := addr.As4()[3]
			if last != 0 && last != 255 {
				out = append(out, addr.String())
			}
		} else {
			out = append(out, addr.String())
		}
		if max > 0 && len(out) >= max {
			return out
		}
		next := addr.Next()
		if !next.IsValid() || next.Compare(addr) <= 0 {
			break
		}
		addr = next
	}
	return out
}

func probeIP(ctx context.Context, ip string, port int, serverName string, mode Mode, rounds int, timeout time.Duration) Result {
	var vals []float64
	errs := map[string]bool{}
	for i := 0; i < rounds; i++ {
		var ms float64
		var err error
		switch mode {
		case ModeQUIC:
			ms, err = probeQUIC(ctx, ip, port, serverName, timeout)
		default:
			ms, err = probeTCP(ctx, ip, port, timeout)
		}
		if err != nil {
			errs[shortErr(err)] = true
		} else {
			vals = append(vals, ms)
		}
	}
	result := summarize(vals)
	result.IP = ip
	result.Protocol = string(mode)
	result.OK = len(vals)
	result.Fail = rounds - len(vals)
	result.When = time.Now().UTC()
	for e := range errs {
		result.Errors = append(result.Errors, e)
	}
	sort.Strings(result.Errors)
	result.Score = score(result)
	return result
}

func probeTCP(ctx context.Context, ip string, port int, timeout time.Duration) (float64, error) {
	dialer := net.Dialer{Timeout: timeout}
	t0 := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return float64(time.Since(t0).Microseconds()) / 1000.0, nil
}

func probeQUIC(ctx context.Context, ip string, port int, serverName string, timeout time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t0 := time.Now()
	conn, err := quic.DialAddr(ctx, net.JoinHostPort(ip, fmt.Sprint(port)), &tls.Config{
		ServerName: quicServerName(serverName),
		NextProtos: []string{
			"argotunnel",
		},
		MinVersion: tls.VersionTLS13,
	}, &quic.Config{
		HandshakeIdleTimeout: timeout,
		MaxIdleTimeout:       timeout,
	})
	if err != nil {
		return 0, err
	}
	_ = conn.CloseWithError(0, "probe complete")
	return float64(time.Since(t0).Microseconds()) / 1000.0, nil
}

func quicServerName(serverName string) string {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" || strings.HasSuffix(serverName, ".v2.argotunnel.com") {
		return config.CloudflaredQUICServerName
	}
	return serverName
}

func summarize(vals []float64) Result {
	if len(vals) == 0 {
		return Result{MedianMS: 999999, MeanMS: 999999, MinMS: 999999, MaxMS: 999999, StdevMS: 999999}
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	var sum float64
	for _, v := range cp {
		sum += v
	}
	mean := sum / float64(len(cp))
	variance := 0.0
	for _, v := range cp {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(cp))
	median := cp[len(cp)/2]
	if len(cp)%2 == 0 {
		median = (cp[len(cp)/2-1] + cp[len(cp)/2]) / 2
	}
	return Result{
		MedianMS: median,
		MeanMS:   mean,
		MinMS:    cp[0],
		MaxMS:    cp[len(cp)-1],
		StdevMS:  math.Sqrt(variance),
	}
}

func score(r Result) float64 {
	if r.OK == 0 {
		return math.Inf(1)
	}
	return r.MedianMS + r.StdevMS*0.35 + r.MaxMS*0.05 + float64(r.Fail)*1000
}

func better(a, b Result) bool {
	if a.OK != b.OK {
		return a.OK > b.OK
	}
	if a.Fail != b.Fail {
		return a.Fail < b.Fail
	}
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.IP < b.IP
}

func uniqueTop(rows []Result, n int) []Result {
	var top []Result
	seen := map[string]bool{}
	for _, r := range rows {
		if r.OK == 0 || seen[r.IP] {
			continue
		}
		seen[r.IP] = true
		top = append(top, r)
		if len(top) >= n {
			break
		}
	}
	return top
}

func minTop(topN, hostnames int) int {
	if hostnames <= 0 {
		hostnames = 2
	}
	if topN <= 0 || topN > hostnames {
		topN = hostnames
	}
	if topN > 2 {
		return 2
	}
	return topN
}

func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}
