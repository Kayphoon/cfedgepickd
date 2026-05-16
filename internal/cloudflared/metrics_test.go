package cloudflared

import (
	"strings"
	"testing"
)

func TestParseMetrics(t *testing.T) {
	input := `
# HELP cloudflared_tunnel_ha_connections Number of active ha connections
cloudflared_tunnel_ha_connections 2
cloudflared_tunnel_concurrent_requests_per_tunnel 0
cloudflared_tunnel_total_requests 123
cloudflared_tunnel_request_errors 4
cloudflared_tunnel_server_locations{connection_id="0",edge_location="lax01"} 1
cloudflared_tunnel_server_locations{connection_id="1",edge_location="lax05"} 1
`
	m, err := ParseMetrics(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if m.HAConnections != 2 || m.ConcurrentRequests != 0 || m.TotalRequests != 123 || m.RequestErrors != 4 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
	if m.ServerLocations["0"] != "lax01" || m.ServerLocations["1"] != "lax05" {
		t.Fatalf("unexpected locations: %+v", m.ServerLocations)
	}
}
