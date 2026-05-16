package hosts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Mapping struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

func Current(path string, hostnames []string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, h := range hostnames {
		want[h] = true
	}
	result := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, h := range fields[1:] {
			if want[h] {
				result[h] = fields[0]
			}
		}
	}
	return result, sc.Err()
}

func Update(path, backupDir string, mappings []Mapping) (string, error) {
	if len(mappings) == 0 {
		return "", fmt.Errorf("no host mappings")
	}
	if backupDir == "" {
		backupDir = filepath.Dir(path)
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backup := filepath.Join(backupDir, fmt.Sprintf("hosts.bak-%s", time.Now().UTC().Format("20060102150405")))
	if err := os.WriteFile(backup, data, 0644); err != nil {
		return "", err
	}

	ipByHost := map[string]string{}
	for _, m := range mappings {
		ipByHost[m.Hostname] = m.IP
	}
	seen := map[string]bool{}
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		original := sc.Text()
		line := strings.TrimSpace(original)
		if line == "" || strings.HasPrefix(line, "#") {
			lines = append(lines, original)
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			lines = append(lines, original)
			continue
		}
		kept := []string{}
		changed := false
		for _, h := range fields[1:] {
			if ip, ok := ipByHost[h]; ok {
				lines = append(lines, fmt.Sprintf("%s %s", ip, h))
				seen[h] = true
				changed = true
			} else {
				kept = append(kept, h)
			}
		}
		if len(kept) > 0 {
			lines = append(lines, strings.Join(append([]string{fields[0]}, kept...), "\t"))
		} else if !changed {
			lines = append(lines, original)
		}
	}
	if err := sc.Err(); err != nil {
		return backup, err
	}
	for _, m := range mappings {
		if !seen[m.Hostname] {
			lines = append(lines, fmt.Sprintf("%s %s", m.IP, m.Hostname))
		}
	}
	next := strings.Join(lines, "\n")
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	tmp := path + ".cfedgepickd.tmp"
	if err := os.WriteFile(tmp, []byte(next), 0644); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return backup, err
	}
	return backup, nil
}

func Restore(path, backup string) error {
	if backup == "" {
		return fmt.Errorf("backup path is empty")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	tmp := path + ".cfedgepickd.restore.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func Mappings(hostnames []string, ips []string) []Mapping {
	var out []Mapping
	for i, h := range hostnames {
		if len(ips) == 0 {
			break
		}
		out = append(out, Mapping{Hostname: h, IP: ips[i%len(ips)]})
	}
	return out
}
