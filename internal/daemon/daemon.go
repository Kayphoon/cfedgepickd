package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kayphoon/cfpick/internal/cloudflared"
	"github.com/kayphoon/cfpick/internal/config"
	"github.com/kayphoon/cfpick/internal/history"
	"github.com/kayphoon/cfpick/internal/hosts"
	"github.com/kayphoon/cfpick/internal/probe"
	"github.com/kayphoon/cfpick/internal/slots"
	"github.com/kayphoon/cfpick/internal/state"
	"github.com/kayphoon/cfpick/internal/switcher"
)

type Decision struct {
	Protocol     string              `json:"protocol"`
	TopIPs       []string            `json:"top_ips"`
	CurrentIPs   []string            `json:"current_ips"`
	Forced       bool                `json:"forced,omitempty"`
	Strategy     string              `json:"strategy,omitempty"`
	ActiveSlot   string              `json:"active_slot,omitempty"`
	MetricsURL   string              `json:"metrics_url,omitempty"`
	ReadyURL     string              `json:"ready_url,omitempty"`
	Idle         bool                `json:"idle"`
	DegradedRaw  bool                `json:"degraded_raw"`
	Degraded     bool                `json:"degraded"`
	DegradedRun  int                 `json:"degraded_run"`
	InCooldown   bool                `json:"in_cooldown"`
	ShouldSwitch bool                `json:"should_switch"`
	Reason       string              `json:"reason"`
	Metrics      cloudflared.Metrics `json:"metrics"`
	Ready        cloudflared.Ready   `json:"ready"`
	Probe        probe.Report        `json:"probe"`
}

func Once(ctx context.Context, cfg config.Config, apply bool) (Decision, *switcher.Result, error) {
	cfg = cfg.WithDefaults()
	endpoint := slots.ResolveActiveEndpoint(ctx, cfg)
	if endpoint.MetricsURL != "" {
		cfg.Cloudflared.MetricsURL = endpoint.MetricsURL
	}
	if endpoint.ReadyURL != "" {
		cfg.Cloudflared.ReadyURL = endpoint.ReadyURL
	}
	if cfg.Switching.Strategy == config.SwitchStrategyRestart && endpoint.Slot.Service != "" && endpoint.Slot.Name != "config" {
		cfg.Cloudflared.Service = endpoint.Slot.Service
	}
	st, _ := state.Load(cfg.Runtime.StateFile)
	metrics, err := cloudflared.FetchMetrics(ctx, cfg.Cloudflared.MetricsURL)
	if err != nil {
		return Decision{}, nil, err
	}
	ready, err := cloudflared.FetchReady(ctx, cfg.Cloudflared.ReadyURL)
	if err != nil {
		return Decision{}, nil, err
	}
	conns, _ := cloudflared.CurrentEdges(ctx, cfg.Edge.Port)
	currentIPs := edgeIPs(conns)
	if len(currentIPs) == 0 {
		currentIPs = currentHostIPs(cfg)
	}
	pr, err := probe.Run(ctx, cfg, probe.Mode(cfg.Cloudflared.Protocol))
	if err != nil {
		return Decision{}, nil, err
	}
	topIPs := topIPs(pr.Top)
	now := time.Now()
	idle, idleSince := idleState(st, metrics, cfg, now)
	errorsIncreasing := !st.LastProbeAt.IsZero() && metrics.RequestErrors > st.LastRequestErrors
	rawDegraded := ready.ReadyConnections < 2 || metrics.HAConnections < 2 || errorsIncreasing
	if !rawDegraded && len(currentIPs) > 0 {
		rawDegraded = currentWorseThanTop(currentIPs, pr.Results, cfg)
	}
	emergencyIP, emergencyRTT, emergencyRTTExceeded := currentRTTAboveThreshold(currentIPs, pr.Results, cfg.Switching.EmergencyRTTThresholdMS)
	if emergencyRTTExceeded {
		rawDegraded = true
	}
	degradedRun := 0
	if rawDegraded {
		degradedRun = st.DegradedStreak + 1
	}
	emergency := ready.ReadyConnections < 2 || emergencyRTTExceeded
	degraded := emergency || degradedRun >= cfg.Switching.DegradedRounds
	cooldown := state.InCooldown(st, cfg.Switching.CooldownSeconds, now)
	changed := differentSet(currentIPs, topIPs)
	should := changed && degraded && !cooldown && (idle || !cfg.Switching.RequireIdleForPlanned)
	if emergency && cfg.Switching.AllowEmergencySwitch && !cooldown {
		should = changed
	}
	reason := "observing"
	if !changed {
		reason = "top IPs already match current active set"
	} else if rawDegraded && !degraded {
		reason = "waiting for degraded confirmation"
	} else if !degraded {
		reason = "current connections are not degraded"
	} else if cooldown {
		reason = "switch cooldown is active"
	} else if emergencyRTTExceeded && cfg.Switching.AllowEmergencySwitch {
		reason = fmt.Sprintf("emergency RTT threshold exceeded: %s %.2fms > %.2fms", emergencyIP, emergencyRTT, cfg.Switching.EmergencyRTTThresholdMS)
	} else if !idle && cfg.Switching.RequireIdleForPlanned {
		reason = "waiting for idle window"
	} else if should {
		reason = "eligible for switch"
	}
	decision := Decision{
		Protocol:     pr.EffectiveProtocol,
		TopIPs:       topIPs,
		CurrentIPs:   currentIPs,
		Strategy:     cfg.Switching.Strategy,
		ActiveSlot:   endpoint.Slot.Name,
		MetricsURL:   cfg.Cloudflared.MetricsURL,
		ReadyURL:     cfg.Cloudflared.ReadyURL,
		Idle:         idle,
		DegradedRaw:  rawDegraded,
		Degraded:     degraded,
		DegradedRun:  degradedRun,
		InCooldown:   cooldown,
		ShouldSwitch: should,
		Reason:       reason,
		Metrics:      metrics,
		Ready:        ready,
		Probe:        pr,
	}

	st.LastProbeAt = now
	st.CurrentIPs = currentIPs
	st.PendingIPs = topIPs
	st.LastTotalRequests = metrics.TotalRequests
	st.LastRequestErrors = metrics.RequestErrors
	st.IdleSince = idleSince
	st.DegradedStreak = degradedRun

	var sw *switcher.Result
	if should && apply {
		cfg.Runtime.DryRun = false
		appliedProtocol := protocolForCloudflaredConfig(cfg, pr)
		res, err := switcher.Apply(ctx, cfg, topIPs, appliedProtocol)
		sw = &res
		st.LastSwitchAt = time.Now()
		st.LastSwitchOK = err == nil
		st.LastProtocol = appliedProtocol
		if err != nil {
			_ = appendHistory(cfg, decision, sw, err.Error())
			_ = state.Save(cfg.Runtime.StateFile, st)
			return decision, sw, err
		}
	}
	_ = appendHistory(cfg, decision, sw, "")
	_ = state.Save(cfg.Runtime.StateFile, st)
	return decision, sw, nil
}

func SwitchNow(ctx context.Context, cfg config.Config, ips []string, apply bool) (Decision, *switcher.Result, error) {
	cfg = cfg.WithDefaults()
	endpoint := slots.ResolveActiveEndpoint(ctx, cfg)
	if endpoint.MetricsURL != "" {
		cfg.Cloudflared.MetricsURL = endpoint.MetricsURL
	}
	if endpoint.ReadyURL != "" {
		cfg.Cloudflared.ReadyURL = endpoint.ReadyURL
	}
	if cfg.Switching.Strategy == config.SwitchStrategyRestart && endpoint.Slot.Service != "" && endpoint.Slot.Name != "config" {
		cfg.Cloudflared.Service = endpoint.Slot.Service
	}
	st, _ := state.Load(cfg.Runtime.StateFile)
	metrics, metricsErr := cloudflared.FetchMetrics(ctx, cfg.Cloudflared.MetricsURL)
	ready, readyErr := cloudflared.FetchReady(ctx, cfg.Cloudflared.ReadyURL)
	conns, _ := cloudflared.CurrentEdges(ctx, cfg.Edge.Port)
	currentIPs := edgeIPs(conns)
	if len(currentIPs) == 0 {
		currentIPs = currentHostIPs(cfg)
	}

	var pr probe.Report
	var err error
	if len(ips) > 0 {
		pr = manualProbeReport(ips, cfg.Cloudflared.Protocol)
	} else {
		pr, err = probe.Run(ctx, cfg, probe.Mode(cfg.Cloudflared.Protocol))
		if err != nil {
			return Decision{}, nil, err
		}
		ips = topIPs(pr.Top)
	}
	if len(ips) == 0 {
		return Decision{}, nil, fmt.Errorf("no IPs selected for switch")
	}

	reason := "manual switch requested"
	if metricsErr != nil || readyErr != nil {
		var notes []string
		if metricsErr != nil {
			notes = append(notes, "metrics unavailable: "+metricsErr.Error())
		}
		if readyErr != nil {
			notes = append(notes, "ready unavailable: "+readyErr.Error())
		}
		reason += " (" + strings.Join(notes, "; ") + ")"
	}

	effectiveProtocol := pr.EffectiveProtocol
	if effectiveProtocol == "" {
		effectiveProtocol = cfg.Cloudflared.Protocol
	}
	if effectiveProtocol == "" {
		effectiveProtocol = config.ProtocolAuto
	}
	decision := Decision{
		Protocol:     effectiveProtocol,
		TopIPs:       ips,
		CurrentIPs:   currentIPs,
		Forced:       true,
		Strategy:     cfg.Switching.Strategy,
		ActiveSlot:   endpoint.Slot.Name,
		MetricsURL:   cfg.Cloudflared.MetricsURL,
		ReadyURL:     cfg.Cloudflared.ReadyURL,
		Idle:         true,
		DegradedRaw:  true,
		Degraded:     true,
		DegradedRun:  cfg.Switching.DegradedRounds,
		InCooldown:   false,
		ShouldSwitch: true,
		Reason:       reason,
		Metrics:      metrics,
		Ready:        ready,
		Probe:        pr,
	}

	cfg.Runtime.DryRun = !apply
	appliedProtocol := protocolForCloudflaredConfig(cfg, pr)
	res, switchErr := switcher.Apply(ctx, cfg, ips, appliedProtocol)
	sw := &res
	if apply {
		st.LastSwitchAt = time.Now()
		st.LastSwitchOK = switchErr == nil
		st.LastProtocol = appliedProtocol
	}
	st.LastProbeAt = time.Now()
	st.CurrentIPs = currentIPs
	st.PendingIPs = ips
	st.LastTotalRequests = metrics.TotalRequests
	st.LastRequestErrors = metrics.RequestErrors
	st.DegradedStreak = cfg.Switching.DegradedRounds

	switchErrText := ""
	if switchErr != nil {
		switchErrText = switchErr.Error()
	}
	_ = appendHistory(cfg, decision, sw, switchErrText)
	_ = state.Save(cfg.Runtime.StateFile, st)
	return decision, sw, switchErr
}

func manualProbeReport(ips []string, protocol string) probe.Report {
	if protocol == "" {
		protocol = config.ProtocolAuto
	}
	when := time.Now().UTC()
	results := make([]probe.Result, 0, len(ips))
	for _, ip := range ips {
		results = append(results, probe.Result{
			IP:       ip,
			Protocol: protocol,
			When:     when,
		})
	}
	return probe.Report{
		Mode:              protocol,
		EffectiveProtocol: protocol,
		Candidates:        len(ips),
		Results:           results,
		Top:               results,
	}
}

func appendHistory(cfg config.Config, decision Decision, sw *switcher.Result, switchErr string) error {
	record := history.Record{
		Time:                    time.Now().UTC(),
		ConfigProtocol:          cfg.Cloudflared.Protocol,
		EffectiveProtocol:       decision.Protocol,
		SwitchStrategy:          decision.Strategy,
		ActiveSlot:              decision.ActiveSlot,
		MetricsURL:              decision.MetricsURL,
		ReadyURL:                decision.ReadyURL,
		ReadyConnections:        decision.Ready.ReadyConnections,
		HAConnections:           decision.Metrics.HAConnections,
		ConcurrentRequests:      decision.Metrics.ConcurrentRequests,
		TotalRequests:           decision.Metrics.TotalRequests,
		RequestErrors:           decision.Metrics.RequestErrors,
		ResponseByCode:          decision.Metrics.ResponseByCode,
		Response2xx:             decision.Metrics.Response2xx,
		Response3xx:             decision.Metrics.Response3xx,
		Response4xx:             decision.Metrics.Response4xx,
		Response5xx:             decision.Metrics.Response5xx,
		ProxyConnectLatencySum:  decision.Metrics.ProxyConnectLatencySum,
		ProxyConnectLatencyHits: decision.Metrics.ProxyConnectLatencyHits,
		TCPActiveSessions:       decision.Metrics.TCPActiveSessions,
		TCPTotalSessions:        decision.Metrics.TCPTotalSessions,
		UDPActiveSessions:       decision.Metrics.UDPActiveSessions,
		UDPTotalSessions:        decision.Metrics.UDPTotalSessions,
		ProcessCPUSeconds:       decision.Metrics.ProcessCPUSeconds,
		ProcessRSSBytes:         decision.Metrics.ProcessRSSBytes,
		ProcessNetworkRxBytes:   decision.Metrics.ProcessNetworkRxBytes,
		ProcessNetworkTxBytes:   decision.Metrics.ProcessNetworkTxBytes,
		GoGoroutines:            decision.Metrics.GoGoroutines,
		GoThreads:               decision.Metrics.GoThreads,
		GoHeapAllocBytes:        decision.Metrics.GoHeapAllocBytes,
		TopIPs:                  decision.TopIPs,
		CurrentIPs:              decision.CurrentIPs,
		TopProbeResults:         probeSamples(decision.Probe.Top),
		CurrentProbeResults:     currentProbeSamples(decision.CurrentIPs, decision.Probe.Results),
		Idle:                    decision.Idle,
		DegradedRaw:             decision.DegradedRaw,
		Degraded:                decision.Degraded,
		ShouldSwitch:            decision.ShouldSwitch,
		Reason:                  decision.Reason,
		SwitchError:             switchErr,
	}
	if len(decision.Probe.Top) > 0 {
		record.TopIP = decision.Probe.Top[0].IP
		record.TopMedianMS = decision.Probe.Top[0].MedianMS
	}
	if sw != nil {
		record.SwitchApplied = sw.Applied
	}
	if err := history.Append(cfg.Runtime.HistoryFile, record); err != nil {
		return err
	}
	if cfg.Runtime.HistoryRetentionDays > 0 {
		cutoff := record.Time.Add(-time.Duration(cfg.Runtime.HistoryRetentionDays) * 24 * time.Hour)
		_, err := history.PruneBefore(cfg.Runtime.HistoryFile, cutoff)
		return err
	}
	return nil
}

func probeSamples(results []probe.Result) []history.IPProbe {
	out := make([]history.IPProbe, 0, len(results))
	for _, r := range results {
		out = append(out, history.IPProbe{
			IP:       r.IP,
			Protocol: r.Protocol,
			OK:       r.OK,
			Fail:     r.Fail,
			MedianMS: r.MedianMS,
			MeanMS:   r.MeanMS,
			MinMS:    r.MinMS,
			MaxMS:    r.MaxMS,
			Score:    r.Score,
		})
	}
	return out
}

func currentProbeSamples(current []string, results []probe.Result) []history.IPProbe {
	byIP := map[string]probe.Result{}
	for _, r := range results {
		byIP[r.IP] = r
	}
	out := make([]history.IPProbe, 0, len(current))
	seen := map[string]bool{}
	for _, ip := range current {
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		r, ok := byIP[ip]
		if !ok {
			out = append(out, history.IPProbe{IP: ip})
			continue
		}
		out = append(out, probeSamples([]probe.Result{r})...)
	}
	return out
}

func protocolForCloudflaredConfig(cfg config.Config, pr probe.Report) string {
	if cfg.Cloudflared.Protocol == config.ProtocolAuto {
		return config.ProtocolAuto
	}
	if cfg.Cloudflared.Protocol != "" {
		return cfg.Cloudflared.Protocol
	}
	if pr.EffectiveProtocol != "" {
		return pr.EffectiveProtocol
	}
	return config.ProtocolAuto
}

func Run(ctx context.Context, cfg config.Config) error {
	cfg = cfg.WithDefaults()
	interval := time.Duration(cfg.Switching.ProbeIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		decision, sw, err := Once(ctx, cfg, !cfg.Runtime.DryRun)
		if err != nil {
			log.Printf("cycle error: %v", err)
		} else {
			data, _ := json.Marshal(decision)
			log.Printf("decision=%s", data)
			if sw != nil {
				data, _ := json.Marshal(sw)
				log.Printf("switch=%s", data)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func edgeIPs(conns []cloudflared.EdgeConnection) []string {
	seen := map[string]bool{}
	var ips []string
	for _, c := range conns {
		if c.IP != "" && !seen[c.IP] {
			seen[c.IP] = true
			ips = append(ips, c.IP)
		}
	}
	return ips
}

func currentHostIPs(cfg config.Config) []string {
	current, err := hosts.Current(cfg.Runtime.HostsFile, cfg.Edge.Hostnames)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var ips []string
	for _, h := range cfg.Edge.Hostnames {
		ip := current[h]
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		ips = append(ips, ip)
	}
	return ips
}

func topIPs(rows []probe.Result) []string {
	var ips []string
	for _, r := range rows {
		ips = append(ips, r.IP)
	}
	return ips
}

func idleState(st state.State, m cloudflared.Metrics, cfg config.Config, now time.Time) (bool, time.Time) {
	if m.ConcurrentRequests > 0 {
		return false, time.Time{}
	}
	if st.LastProbeAt.IsZero() {
		return false, time.Time{}
	}
	if m.TotalRequests != st.LastTotalRequests {
		return false, time.Time{}
	}
	since := st.IdleSince
	if since.IsZero() {
		since = now
	}
	window := time.Duration(cfg.Switching.IdleWindowSeconds) * time.Second
	if window <= 0 {
		return true, since
	}
	return now.Sub(since) >= window, since
}

func currentWorseThanTop(current []string, results []probe.Result, cfg config.Config) bool {
	if len(results) == 0 || len(current) == 0 {
		return false
	}
	best := results[0].MedianMS
	if best <= 0 {
		return false
	}
	threshold := best * cfg.Switching.DegradedFactor
	byIP := map[string]probe.Result{}
	for _, r := range results {
		byIP[r.IP] = r
	}
	for _, ip := range current {
		r, found := byIP[ip]
		if !found {
			continue
		}
		if r.MedianMS > threshold || r.OK == 0 {
			return true
		}
	}
	return false
}

func currentRTTAboveThreshold(current []string, results []probe.Result, thresholdMS float64) (string, float64, bool) {
	if thresholdMS <= 0 || len(current) == 0 || len(results) == 0 {
		return "", 0, false
	}
	byIP := map[string]probe.Result{}
	for _, r := range results {
		byIP[r.IP] = r
	}
	var worstIP string
	var worstRTT float64
	for _, ip := range current {
		r, found := byIP[ip]
		if !found || r.MedianMS <= thresholdMS {
			continue
		}
		if r.MedianMS > worstRTT {
			worstIP = ip
			worstRTT = r.MedianMS
		}
	}
	return worstIP, worstRTT, worstIP != ""
}

func differentSet(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) != len(b)
	}
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if !set[x] {
			return true
		}
	}
	return false
}

func PrintDecision(decision Decision, sw *switcher.Result) {
	data, _ := json.MarshalIndent(decision, "", "  ")
	fmt.Println(string(data))
	if sw != nil {
		data, _ := json.MarshalIndent(sw, "", "  ")
		fmt.Println(string(data))
	}
}

func MappingsForDecision(d Decision, cfg config.Config) []hosts.Mapping {
	return hosts.Mappings(cfg.Edge.Hostnames, d.TopIPs)
}
