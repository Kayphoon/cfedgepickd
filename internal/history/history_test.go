package history

import (
	"strings"
	"testing"
	"time"
)

func TestSeriesRequestDelta(t *testing.T) {
	now := time.Unix(100, 0)
	records := []Record{
		{Time: now, TotalRequests: 10},
		{Time: now.Add(time.Minute), TotalRequests: 15},
		{Time: now.Add(2 * time.Minute), TotalRequests: 14},
		{Time: now.Add(3 * time.Minute), TotalRequests: 20},
	}

	points, sum, err := Series(records, "request_delta")
	if err != nil {
		t.Fatalf("Series returned error: %v", err)
	}
	if len(points) != 4 {
		t.Fatalf("points=%d", len(points))
	}
	want := []float64{0, 5, 0, 6}
	for i, p := range points {
		if p.Value != want[i] {
			t.Fatalf("point[%d]=%v, want %v", i, p.Value, want[i])
		}
	}
	if sum.Latest != 6 || sum.Max != 6 {
		t.Fatalf("summary=%+v", sum)
	}
}

func TestSeriesMetricAliases(t *testing.T) {
	records := []Record{{Time: time.Unix(100, 0), TopMedianMS: 42}}

	points, sum, err := Series(records, "top_median_ms")
	if err != nil {
		t.Fatalf("Series returned error: %v", err)
	}
	if sum.Metric != "rtt" || len(points) != 1 || points[0].Value != 42 {
		t.Fatalf("points=%+v summary=%+v", points, sum)
	}
}

func TestSeriesRejectsUnknownMetric(t *testing.T) {
	if _, _, err := Series([]Record{{Time: time.Unix(100, 0)}}, "nope"); err == nil {
		t.Fatal("expected unknown metric error")
	}
}

func TestRenderASCII(t *testing.T) {
	now := time.Unix(100, 0)
	points := []Point{
		{Time: now, Value: 1},
		{Time: now.Add(time.Minute), Value: 3},
	}
	sum := Summary{
		Metric: "rtt",
		Count:  2,
		Min:    1,
		Max:    3,
		Avg:    2,
		Latest: 3,
		From:   points[0].Time,
		To:     points[1].Time,
	}

	out, err := RenderASCII(points, sum, 20, 4)
	if err != nil {
		t.Fatalf("RenderASCII returned error: %v", err)
	}
	if !strings.Contains(out, "metric=rtt") || !strings.Contains(out, "*") {
		t.Fatalf("unexpected graph output:\n%s", out)
	}
}
