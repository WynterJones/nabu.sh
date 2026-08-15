package remoteaccess

import "testing"

func TestParseTailscaleStatus(t *testing.T) {
	status := parseTailscaleStatus([]byte(`{
		"Version":"1.94.2",
		"BackendState":"Running",
		"Self":{"HostName":"Nabu Mac","DNSName":"nabu.example.ts.net."}
	}`))
	if !status.Connected || status.Version != "1.94.2" || status.DNSName != "nabu.example.ts.net" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.TailnetName != "example.ts.net" || status.PrivateURL != "https://nabu.example.ts.net" {
		t.Fatalf("unexpected address: %+v", status)
	}
}

func TestConfigured(t *testing.T) {
	for _, input := range []string{"", "{}", "null", "  {}  "} {
		if configured([]byte(input)) {
			t.Fatalf("empty config %q reported configured", input)
		}
	}
	if !configured([]byte(`{"Web":{"nabu.example.ts.net:443":{}}}`)) {
		t.Fatal("serve config was not detected")
	}
}

func TestNabuConfiguredRequiresExactLocalTarget(t *testing.T) {
	if nabuConfigured([]byte(`{"Web":{"nabu.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`)) {
		t.Fatal("unrelated Serve target reported as Nabu")
	}
	if !nabuConfigured([]byte(`{"Web":{"nabu.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}}}}`)) {
		t.Fatal("Nabu Serve target was not detected")
	}
}

func TestContainsOtherProxyTarget(t *testing.T) {
	if containsOtherProxyTarget([]byte(`{"Web":{"nabu.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"}}}}}`)) {
		t.Fatal("Nabu-only configuration reported another target")
	}
	if !containsOtherProxyTarget([]byte(`{"Web":{"nabu.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7777"},"/other":{"Proxy":"http://127.0.0.1:3000"}}}}}`)) {
		t.Fatal("shared Serve configuration did not report another target")
	}
	if !containsOtherProxyTarget([]byte(`not-json`)) {
		t.Fatal("malformed Serve configuration did not fail closed")
	}
}

func TestParseAuthorizationURL(t *testing.T) {
	output := []byte("Serve is not enabled.\nTo enable, visit:\nhttps://login.tailscale.com/f/serve?node=abc123\n")
	if got := parseAuthorizationURL(output); got != "https://login.tailscale.com/f/serve?node=abc123" {
		t.Fatalf("unexpected authorization URL %q", got)
	}
	if got := parseAuthorizationURL([]byte("https://attacker.example/f/serve")); got != "" {
		t.Fatalf("accepted untrusted authorization URL %q", got)
	}
}
