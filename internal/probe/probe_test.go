package probe

import "testing"

func TestUniqueTop(t *testing.T) {
	rows := []Result{
		{IP: "1.1.1.1", OK: 3, MedianMS: 1, Score: 1},
		{IP: "1.1.1.1", OK: 3, MedianMS: 2, Score: 2},
		{IP: "1.1.1.2", OK: 3, MedianMS: 2, Score: 2},
		{IP: "1.1.1.3", OK: 0, MedianMS: 0, Score: 0},
	}
	top := uniqueTop(rows, 2)
	if len(top) != 2 {
		t.Fatalf("len=%d", len(top))
	}
	if top[0].IP != "1.1.1.1" || top[1].IP != "1.1.1.2" {
		t.Fatalf("unexpected top: %+v", top)
	}
}

func TestQUICServerNameUsesCloudflaredEdgeName(t *testing.T) {
	if got := quicServerName(""); got != "quic.cftunnel.com" {
		t.Fatalf("empty server name = %q", got)
	}
	if got := quicServerName("region1.v2.argotunnel.com"); got != "quic.cftunnel.com" {
		t.Fatalf("legacy region server name = %q", got)
	}
	if got := quicServerName("custom.example.com"); got != "custom.example.com" {
		t.Fatalf("custom server name = %q", got)
	}
}
