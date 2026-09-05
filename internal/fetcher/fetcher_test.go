package fetcher

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"subconv-next/internal/model"
)

func TestFetchSuccessAndCacheFallback(t *testing.T) {
	callCount := 0

	f := New(Options{
		CacheDir:          t.TempDir(),
		Timeout:           5 * time.Second,
		MaxBodyBytes:      1024,
		MaxRedirects:      3,
		AllowPrivateHosts: true,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
		RequestDoer: func(ctx context.Context, target *url.URL, resolvedIP net.IP, source Source) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"text/plain"},
					},
					Body: io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#cached")),
				}, nil
			}
			return nil, errors.New("network down")
		},
	})

	source := Source{
		Name:      "demo",
		URL:       "https://example.com/subscription",
		UserAgent: "SubConvNext/0.1",
		Enabled:   true,
	}

	first, warnings, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if first.FromCache {
		t.Fatalf("first.FromCache = true, want false")
	}

	second, warnings, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("Fetch() cache fallback error = %v", err)
	}
	if !second.FromCache {
		t.Fatalf("second.FromCache = false, want true")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one cache warning", warnings)
	}
}

func TestFetchClosesPerRequestTransportConnections(t *testing.T) {
	var openConnections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("subscription"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			openConnections.Add(1)
		case http.StateClosed:
			openConnections.Add(-1)
		}
	}
	server.Start()
	defer server.Close()

	f := New(Options{
		CacheDir:          t.TempDir(),
		Timeout:           2 * time.Second,
		MaxBodyBytes:      1024,
		AllowPrivateHosts: true,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
		},
	})
	source := Source{Name: "transport-lifecycle", URL: server.URL, Enabled: true, AllowPrivateHosts: true}
	for i := 0; i < 20; i++ {
		if _, _, err := f.Fetch(context.Background(), source); err != nil {
			t.Fatalf("Fetch() error on request %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for openConnections.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := openConnections.Load(); got != 0 {
		t.Fatalf("open connections after fetches = %d, want 0", got)
	}
}

func TestFetchUsesFreshCacheBeforeNetwork(t *testing.T) {
	callCount := 0
	f := New(Options{
		CacheDir:     t.TempDir(),
		MaxBodyBytes: 1024,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
		RequestDoer: func(context.Context, *url.URL, net.IP, Source) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("cached body")),
			}, nil
		},
	})
	source := Source{
		Name:     "rules",
		URL:      "https://example.com/rules.txt",
		Enabled:  true,
		CacheTTL: time.Hour,
	}

	first, _, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	second, warnings, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("network calls = %d, want 1", callCount)
	}
	if first.FromCache || !second.FromCache || string(second.Content) != "cached body" {
		t.Fatalf("cache results = first:%+v second:%+v", first, second)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "fresh cached content") {
		t.Fatalf("warnings = %#v, want fresh cache diagnostic", warnings)
	}
}

func TestFetchRejectsCacheBodyMetadataMismatch(t *testing.T) {
	callCount := 0
	cacheDir := t.TempDir()
	f := New(Options{
		CacheDir:     cacheDir,
		MaxBodyBytes: 1024,
		Resolver:     staticResolver{ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}},
		RequestDoer: func(context.Context, *url.URL, net.IP, Source) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("network body")),
			}, nil
		},
	})
	source := Source{Name: "rules", URL: "https://example.com/rules.txt", Enabled: true, CacheTTL: time.Hour}
	if _, _, err := f.Fetch(context.Background(), source); err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	bodyPath, _ := cachePaths(cacheDir, source.URL)
	if err := os.WriteFile(bodyPath, []byte("tampered body with a different size"), 0600); err != nil {
		t.Fatal(err)
	}
	second, _, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if callCount != 2 || second.FromCache || string(second.Content) != "network body" {
		t.Fatalf("cache mismatch was reused: calls=%d result=%+v", callCount, second)
	}
}

func TestFetchCapturesSubscriptionUserinfoAndDefaultUserAgent(t *testing.T) {
	var gotUserAgent string
	f := New(Options{
		CacheDir:          t.TempDir(),
		Timeout:           5 * time.Second,
		MaxBodyBytes:      1024,
		MaxRedirects:      3,
		AllowPrivateHosts: true,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
		RequestDoer: func(ctx context.Context, target *url.URL, resolvedIP net.IP, source Source) (*http.Response, error) {
			gotUserAgent = userAgentOrDefault(source.UserAgent)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":          []string{"text/plain"},
					"Subscription-Userinfo": []string{"upload=100; download=200; total=1000; expire=1745942400"},
				},
				Body: io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#userinfo")),
			}, nil
		},
	})

	fetched, warnings, err := f.Fetch(context.Background(), Source{
		Name:    "demo",
		URL:     "https://example.com/subscription",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if gotUserAgent != model.DefaultUserAgent {
		t.Fatalf("User-Agent = %q, want %q", gotUserAgent, model.DefaultUserAgent)
	}
	if fetched.SubscriptionUserinfo != "upload=100; download=200; total=1000; expire=1745942400" {
		t.Fatalf("SubscriptionUserinfo = %q, want upstream header", fetched.SubscriptionUserinfo)
	}
}

func TestFetchBlocksPrivateHostsByDefault(t *testing.T) {
	f := New(Options{
		CacheDir:     t.TempDir(),
		Timeout:      5 * time.Second,
		MaxBodyBytes: 1024,
		MaxRedirects: 3,
	})

	_, _, err := f.Fetch(context.Background(), Source{
		Name:      "blocked",
		URL:       "http://localhost/private",
		UserAgent: "SubConvNext/0.1",
		Enabled:   true,
	})
	if err == nil {
		t.Fatalf("Fetch() error = nil, want blocked host error")
	}
}

func TestFetchRejectsURLCredentials(t *testing.T) {
	f := New(Options{CacheDir: t.TempDir()})

	_, _, err := f.Fetch(context.Background(), Source{
		Name:    "credentials",
		URL:     "https://user:password@example.com/subscription",
		Enabled: true,
	})
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("Fetch() error = %v, want URL credentials error", err)
	}
}

func TestPublicModeRejectsNonWebPortsBeforeDNS(t *testing.T) {
	f := New(Options{
		CacheDir:   t.TempDir(),
		PublicMode: true,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
	})
	_, _, err := f.Fetch(context.Background(), Source{
		Name:    "blocked-port",
		URL:     "https://example.com:22/subscription",
		Enabled: true,
	})
	if err == nil || !strings.Contains(err.Error(), "outbound port 22") {
		t.Fatalf("Fetch() error = %v, want public port rejection", err)
	}
}

func TestValidatePublicURLAllowsCommonAlternateHTTPSPort(t *testing.T) {
	if err := ValidatePublicURL("https://example.com:8443/subscription"); err != nil {
		t.Fatalf("ValidatePublicURL(8443) error = %v", err)
	}
	if err := ValidatePublicURL("http://example.com:6379/subscription"); err == nil {
		t.Fatal("ValidatePublicURL(6379) error = nil, want rejection")
	}
}

func TestFetchAllowsPrivateHostWhenSourceAllowsLAN(t *testing.T) {
	f := New(Options{
		CacheDir:     t.TempDir(),
		Timeout:      5 * time.Second,
		MaxBodyBytes: 1024,
		MaxRedirects: 3,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("10.0.0.3")}},
		},
		RequestDoer: func(ctx context.Context, target *url.URL, resolvedIP net.IP, source Source) (*http.Response, error) {
			if got := resolvedIP.String(); got != "10.0.0.3" {
				t.Fatalf("resolvedIP = %q, want 10.0.0.3", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/plain"},
				},
				Body: io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#lan")),
			}, nil
		},
	})

	fetched, _, err := f.Fetch(context.Background(), Source{
		Name:              "lan",
		URL:               "http://10.0.0.3/sub",
		Enabled:           true,
		AllowPrivateHosts: true,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := string(fetched.Content); !strings.Contains(got, "#lan") {
		t.Fatalf("fetched.Content = %q, want lan node", got)
	}
}

func TestOptionsFromConfigUsesServiceAllowLAN(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.AllowLAN = true

	opts := OptionsFromConfig(cfg)

	if !opts.AllowPrivateHosts {
		t.Fatalf("AllowPrivateHosts = false, want true when service.allow_lan is enabled")
	}
}

func TestFetchFallsBackFromEmptyClashYAML(t *testing.T) {
	callUserAgents := []string{}

	f := New(Options{
		CacheDir:          t.TempDir(),
		Timeout:           5 * time.Second,
		MaxBodyBytes:      4096,
		MaxRedirects:      3,
		AllowPrivateHosts: true,
		Resolver: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
		RequestDoer: func(ctx context.Context, target *url.URL, resolvedIP net.IP, source Source) (*http.Response, error) {
			callUserAgents = append(callUserAgents, source.UserAgent)
			if strings.EqualFold(strings.TrimSpace(source.UserAgent), "clash") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"text/plain"},
					},
					Body: io.NopCloser(strings.NewReader("proxies: []\nproxy-groups:\n  - { name: demo, type: select, proxies: [DIRECT] }\nrules:\n  - MATCH,DIRECT\n")),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/plain"},
				},
				Body: io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#fallback")),
			}, nil
		},
	})

	source := Source{
		Name:      "demo",
		URL:       "https://example.com/subscription",
		UserAgent: "clash",
		Enabled:   true,
	}

	fetched, warnings, err := f.Fetch(context.Background(), source)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(callUserAgents) != 2 {
		t.Fatalf("len(callUserAgents) = %d, want 2", len(callUserAgents))
	}
	if callUserAgents[1] != model.DefaultUserAgent {
		t.Fatalf("fallback user-agent = %q, want %q", callUserAgents[1], model.DefaultUserAgent)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "generic user-agent") {
		t.Fatalf("warnings = %#v, want generic user-agent retry warning", warnings)
	}
	if got := string(fetched.Content); !strings.Contains(got, "ss://") {
		t.Fatalf("fetched.Content = %q, want fallback URI content", got)
	}
}

func TestBlockedIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "127.0.0.1", want: true},
		{ip: "10.0.0.1", want: true},
		{ip: "192.168.1.10", want: true},
		{ip: "100.64.0.1", want: true},
		{ip: "192.0.2.1", want: true},
		{ip: "198.18.0.1", want: true},
		{ip: "203.0.113.1", want: true},
		{ip: "64:ff9b::a00:1", want: true},
		{ip: "2002:0a00:0001::", want: true},
		{ip: "8.8.8.8", want: false},
		{ip: "2606:4700:4700::1111", want: false},
	}

	for _, tt := range tests {
		if got := isBlockedIP(netParseIP(tt.ip)); got != tt.want {
			t.Fatalf("isBlockedIP(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func netParseIP(value string) net.IP {
	return net.ParseIP(value)
}

func TestPublicHostResolverBypassesFakeIPDNS(t *testing.T) {
	resolver := publicHostResolver{
		primary: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("198.18.0.125")}},
		},
		fallbacks: []HostResolver{
			staticResolver{ips: []net.IPAddr{{IP: net.ParseIP("104.21.73.232")}}},
		},
	}

	ips, err := resolver.LookupIPAddr(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupIPAddr() error = %v", err)
	}
	if len(ips) != 1 || !ips[0].IP.Equal(net.ParseIP("104.21.73.232")) {
		t.Fatalf("LookupIPAddr() = %#v, want public fallback address", ips)
	}
}

func TestPublicHostResolverKeepsSafePrimaryResult(t *testing.T) {
	resolver := publicHostResolver{
		primary: staticResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		},
		fallbacks: []HostResolver{
			staticResolver{ips: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}},
		},
	}

	ips, err := resolver.LookupIPAddr(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupIPAddr() error = %v", err)
	}
	if len(ips) != 1 || !ips[0].IP.Equal(net.ParseIP("8.8.8.8")) {
		t.Fatalf("LookupIPAddr() = %#v, want primary address", ips)
	}
}

type staticResolver struct {
	ips []net.IPAddr
}

func (r staticResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return append([]net.IPAddr(nil), r.ips...), nil
}
