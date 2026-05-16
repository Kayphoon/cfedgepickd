package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	LastProbeAt       time.Time `json:"last_probe_at,omitempty"`
	LastSwitchAt      time.Time `json:"last_switch_at,omitempty"`
	LastSwitchOK      bool      `json:"last_switch_ok"`
	LastProtocol      string    `json:"last_protocol,omitempty"`
	CurrentIPs        []string  `json:"current_ips,omitempty"`
	PendingIPs        []string  `json:"pending_ips,omitempty"`
	DegradedStreak    int       `json:"degraded_streak"`
	LastTotalRequests float64   `json:"last_total_requests"`
	LastRequestErrors float64   `json:"last_request_errors"`
	IdleSince         time.Time `json:"idle_since,omitempty"`
}

func Load(path string) (State, error) {
	var st State
	if path == "" {
		return st, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func InCooldown(st State, cooldownSeconds int, now time.Time) bool {
	if st.LastSwitchAt.IsZero() || cooldownSeconds <= 0 {
		return false
	}
	return now.Sub(st.LastSwitchAt) < time.Duration(cooldownSeconds)*time.Second
}
