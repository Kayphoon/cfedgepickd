package discover

import "testing"

func TestPickCloudflaredServicePrefersOriginalOverBlue(t *testing.T) {
	units := `tunnelflux-cloudflared-blue.service loaded active running tunnelflux blue cloudflared slot
cloudflared.service loaded inactive dead Cloudflare Tunnel
`
	if got := pickCloudflaredService(units); got != "cloudflared.service" {
		t.Fatalf("service=%q", got)
	}
}

func TestPickCloudflaredServiceSkipsCfpickManagedBlue(t *testing.T) {
	units := `tunnelflux-cloudflared-blue.service loaded active running tunnelflux blue cloudflared slot
other.service loaded active running something
`
	if got := pickCloudflaredService(units); got != "" {
		t.Fatalf("service=%q", got)
	}
}
