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
cloudflared_tunnel_response_by_code{status_code="200"} 100
cloudflared_tunnel_response_by_code{status_code="304"} 10
cloudflared_tunnel_response_by_code{status_code="404"} 2
cloudflared_tunnel_response_by_code{status_code="500"} 1
cloudflared_proxy_connect_latency_sum 50
cloudflared_proxy_connect_latency_count 2
cloudflared_tcp_active_sessions 3
cloudflared_tcp_total_sessions 9
cloudflared_udp_active_sessions 4
cloudflared_udp_total_sessions 10
process_cpu_seconds_total 12.5
process_resident_memory_bytes 10485760
process_network_receive_bytes_total 1000
process_network_transmit_bytes_total 2000
go_goroutines 48
go_threads 9
go_memstats_heap_alloc_bytes 5242880
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
	if m.Response2xx != 100 || m.Response3xx != 10 || m.Response4xx != 2 || m.Response5xx != 1 {
		t.Fatalf("unexpected response classes: %+v", m)
	}
	if m.ResponseByCode["500"] != 1 {
		t.Fatalf("unexpected response codes: %+v", m.ResponseByCode)
	}
	if m.ProxyConnectLatencySum != 50 || m.ProxyConnectLatencyHits != 2 {
		t.Fatalf("unexpected proxy latency: %+v", m)
	}
	if m.TCPActiveSessions != 3 || m.TCPTotalSessions != 9 || m.UDPActiveSessions != 4 || m.UDPTotalSessions != 10 {
		t.Fatalf("unexpected sessions: %+v", m)
	}
	if m.ProcessCPUSeconds != 12.5 || m.ProcessRSSBytes != 10485760 || m.ProcessNetworkRxBytes != 1000 || m.ProcessNetworkTxBytes != 2000 {
		t.Fatalf("unexpected process metrics: %+v", m)
	}
	if m.GoGoroutines != 48 || m.GoThreads != 9 || m.GoHeapAllocBytes != 5242880 {
		t.Fatalf("unexpected go runtime metrics: %+v", m)
	}
	if m.ServerLocations["0"] != "lax01" || m.ServerLocations["1"] != "lax05" {
		t.Fatalf("unexpected locations: %+v", m.ServerLocations)
	}
}

func TestParseSSConnectionsIncludesUDPCloudflaredEdges(t *testing.T) {
	input := `
udp   ESTAB 0      0      10.0.0.10:39102 198.41.200.113:7844 users:(("cloudflared",pid=1234,fd=8))
tcp   ESTAB 0      0      10.0.0.10:39103 198.41.200.114:7844 users:(("cloudflared",pid=1234,fd=9))
tcp   ESTAB 0      0      10.0.0.10:39104 203.0.113.10:443 users:(("cloudflared",pid=1234,fd=10))
udp   ESTAB 0      0      10.0.0.10:39105 198.41.200.115:7844 users:(("other",pid=1235,fd=11))
`
	conns := parseSSConnections(input, 7844)
	if len(conns) != 2 {
		t.Fatalf("expected UDP and TCP cloudflared :7844 edges, got %d: %+v", len(conns), conns)
	}
	if conns[0].Remote != "198.41.200.113:7844" || conns[0].IP != "198.41.200.113" {
		t.Fatalf("unexpected UDP edge: %+v", conns[0])
	}
	if conns[1].Remote != "198.41.200.114:7844" || conns[1].IP != "198.41.200.114" {
		t.Fatalf("unexpected TCP edge: %+v", conns[1])
	}
}

func TestIsPublicRemoteRejectsOriginLoopback(t *testing.T) {
	if isPublicRemote("127.0.0.1:8000") {
		t.Fatal("loopback origin socket should not be treated as an edge")
	}
}

func TestIsPublicRemoteAcceptsPublicEdge(t *testing.T) {
	if !isPublicRemote("198.41.200.227:7844") {
		t.Fatal("public cloudflared peer should be treated as an edge candidate")
	}
}
