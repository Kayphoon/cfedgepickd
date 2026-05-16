package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Record struct {
	Time               time.Time `json:"time"`
	ConfigProtocol     string    `json:"config_protocol"`
	EffectiveProtocol  string    `json:"effective_protocol"`
	ReadyConnections   int       `json:"ready_connections"`
	HAConnections      int       `json:"ha_connections"`
	ConcurrentRequests int       `json:"concurrent_requests"`
	TotalRequests      float64   `json:"total_requests"`
	RequestErrors      float64   `json:"request_errors"`
	TopMedianMS        float64   `json:"top_median_ms"`
	TopIP              string    `json:"top_ip,omitempty"`
	TopIPs             []string  `json:"top_ips,omitempty"`
	CurrentIPs         []string  `json:"current_ips,omitempty"`
	Idle               bool      `json:"idle"`
	DegradedRaw        bool      `json:"degraded_raw"`
	Degraded           bool      `json:"degraded"`
	ShouldSwitch       bool      `json:"should_switch"`
	SwitchApplied      bool      `json:"switch_applied"`
	SwitchError        string    `json:"switch_error,omitempty"`
	Reason             string    `json:"reason,omitempty"`
}

type Point struct {
	Time  time.Time
	Value float64
}

type Summary struct {
	Metric string
	Count  int
	Min    float64
	Max    float64
	Avg    float64
	Latest float64
	From   time.Time
	To     time.Time
}

func Append(path string, r Record) error {
	if path == "" {
		return nil
	}
	if r.Time.IsZero() {
		r.Time = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func ReadSince(path string, since time.Time) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var records []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.Time.IsZero() || r.Time.Before(since) {
			continue
		}
		records = append(records, r)
	}
	return records, sc.Err()
}

func Series(records []Record, metric string) ([]Point, Summary, error) {
	metric, ok := normalizeMetric(metric)
	if !ok {
		return nil, Summary{Metric: metric}, fmt.Errorf("unsupported metric %q", metric)
	}
	var points []Point
	var last Record
	for i, r := range records {
		value, ok := metricValue(r, last, i > 0, metric)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			last = r
			continue
		}
		points = append(points, Point{Time: r.Time, Value: value})
		last = r
	}
	if len(points) == 0 {
		return nil, Summary{Metric: metric}, nil
	}
	sum := Summary{
		Metric: metric,
		Count:  len(points),
		Min:    points[0].Value,
		Max:    points[0].Value,
		Latest: points[len(points)-1].Value,
		From:   points[0].Time,
		To:     points[len(points)-1].Time,
	}
	var total float64
	for _, p := range points {
		if p.Value < sum.Min {
			sum.Min = p.Value
		}
		if p.Value > sum.Max {
			sum.Max = p.Value
		}
		total += p.Value
	}
	sum.Avg = total / float64(len(points))
	return points, sum, nil
}

func metricValue(r, last Record, hasLast bool, metric string) (float64, bool) {
	switch metric {
	case "ready":
		return float64(r.ReadyConnections), true
	case "ha":
		return float64(r.HAConnections), true
	case "concurrent":
		return float64(r.ConcurrentRequests), true
	case "requests":
		return r.TotalRequests, true
	case "request_delta":
		if !hasLast {
			return 0, true
		}
		return nonNegativeDelta(r.TotalRequests, last.TotalRequests), true
	case "errors":
		return r.RequestErrors, true
	case "error_delta":
		if !hasLast {
			return 0, true
		}
		return nonNegativeDelta(r.RequestErrors, last.RequestErrors), true
	case "rtt":
		return r.TopMedianMS, r.TopMedianMS > 0
	case "degraded":
		if r.Degraded {
			return 1, true
		}
		return 0, true
	case "idle":
		if r.Idle {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func normalizeMetric(metric string) (string, bool) {
	metric = strings.TrimSpace(strings.ToLower(metric))
	switch metric {
	case "", "rtt", "top_rtt", "top_median_ms":
		return "rtt", true
	case "ready", "ready_connections":
		return "ready", true
	case "ha", "ha_connections":
		return "ha", true
	case "concurrent", "concurrent_requests":
		return "concurrent", true
	case "requests", "total_requests":
		return "requests", true
	case "request_delta", "requests_delta":
		return "request_delta", true
	case "errors", "request_errors":
		return "errors", true
	case "error_delta", "errors_delta":
		return "error_delta", true
	case "degraded":
		return "degraded", true
	case "idle":
		return "idle", true
	default:
		return metric, false
	}
}

func nonNegativeDelta(now, before float64) float64 {
	if now < before {
		return 0
	}
	return now - before
}

func RenderASCII(points []Point, sum Summary, width, height int) (string, error) {
	if len(points) == 0 {
		return "", errors.New("no history points for selected metric/window")
	}
	if width < 20 {
		width = 20
	}
	if height < 4 {
		height = 4
	}
	if width > 160 {
		width = 160
	}
	if height > 40 {
		height = 40
	}
	points = bucket(points, width)
	minV, maxV := sum.Min, sum.Max
	if minV == maxV {
		minV--
		maxV++
	}
	grid := make([][]byte, height)
	for i := range grid {
		grid[i] = []byte(strings.Repeat(" ", width))
	}
	for i, p := range points {
		x := i
		if x >= width {
			x = width - 1
		}
		ratio := (p.Value - minV) / (maxV - minV)
		y := height - 1 - int(math.Round(ratio*float64(height-1)))
		if y < 0 {
			y = 0
		}
		if y >= height {
			y = height - 1
		}
		grid[y][x] = '*'
	}
	var b strings.Builder
	fmt.Fprintf(&b, "metric=%s points=%d min=%.2f max=%.2f avg=%.2f latest=%.2f\n", sum.Metric, sum.Count, sum.Min, sum.Max, sum.Avg, sum.Latest)
	fmt.Fprintf(&b, "from=%s to=%s\n", sum.From.Local().Format("2006-01-02 15:04:05"), sum.To.Local().Format("2006-01-02 15:04:05"))
	for row := 0; row < height; row++ {
		value := maxV - (maxV-minV)*float64(row)/float64(height-1)
		fmt.Fprintf(&b, "%8.2f | %s\n", value, string(grid[row]))
	}
	fmt.Fprintf(&b, "          +%s\n", strings.Repeat("-", width))
	return b.String(), nil
}

func bucket(points []Point, width int) []Point {
	if len(points) <= width {
		return points
	}
	out := make([]Point, 0, width)
	for i := 0; i < width; i++ {
		start := i * len(points) / width
		end := (i + 1) * len(points) / width
		if end <= start {
			end = start + 1
		}
		var sum float64
		for _, p := range points[start:end] {
			sum += p.Value
		}
		out = append(out, Point{
			Time:  points[end-1].Time,
			Value: sum / float64(end-start),
		})
	}
	return out
}
