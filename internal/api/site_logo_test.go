package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"subconv-next/internal/model"
)

type siteLogoResolverStub struct {
	ips []net.IPAddr
	err error
}

func (stub siteLogoResolverStub) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return stub.ips, stub.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestSiteLogoTargetRejectsPrivateAndCredentialedURLs(t *testing.T) {
	private := siteLogoResolverStub{ips: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
	public := siteLogoResolverStub{ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}

	if err := validateSiteLogoTarget(context.Background(), private, mustParseURL(t, "https://example.com/icon.png")); err == nil {
		t.Fatal("validateSiteLogoTarget() accepted a private redirect target")
	}
	if err := validateSiteLogoTarget(context.Background(), public, mustParseURL(t, "https://user:pass@example.com/icon.png")); err == nil {
		t.Fatal("validateSiteLogoTarget() accepted URL credentials")
	}
	if err := validateSiteLogoTarget(context.Background(), public, mustParseURL(t, "https://example.com:22/icon.png")); err == nil {
		t.Fatal("validateSiteLogoTarget() accepted a non-Web redirect port")
	}
	if err := validateSiteLogoTarget(context.Background(), public, mustParseURL(t, "https://example.com/icon.png")); err != nil {
		t.Fatalf("validateSiteLogoTarget() rejected public target: %v", err)
	}
}

func TestParseSiteLogoURLRejectsCredentials(t *testing.T) {
	if _, err := parseSiteLogoURL("https://user:pass@example.com/"); err == nil {
		t.Fatal("parseSiteLogoURL() accepted URL credentials")
	}
}

func TestResolveSiteLogoDialTargetUsesValidatedIP(t *testing.T) {
	resolver := siteLogoResolverStub{ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}
	got, err := resolveSiteLogoDialTarget(context.Background(), resolver, "example.com:443")
	if err != nil {
		t.Fatalf("resolveSiteLogoDialTarget() error = %v", err)
	}
	if got != "8.8.8.8:443" {
		t.Fatalf("dial target = %q, want public resolved address", got)
	}
}

func TestSiteLogoRejectsReservedPublicLookingNetworks(t *testing.T) {
	for _, rawIP := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "64:ff9b::a00:1", "2002:0a00:0001::"} {
		resolver := siteLogoResolverStub{ips: []net.IPAddr{{IP: net.ParseIP(rawIP)}}}
		if siteLogoHostAllowedWithResolver(context.Background(), resolver, "example.com") {
			t.Fatalf("siteLogoHostAllowedWithResolver() accepted reserved address %s", rawIP)
		}
	}
}

func TestFetchLogoRejectsNonImageAndOversizedBodies(t *testing.T) {
	target := mustParseURL(t, "https://example.com/icon")
	response := func(contentType string, body []byte) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{contentType}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		})}
	}

	if _, ok := fetchLogoAsDataURL(response("text/html", []byte("<html></html>")), target); ok {
		t.Fatal("fetchLogoAsDataURL() accepted non-image content")
	}
	if _, ok := fetchLogoAsDataURL(response("image/png", bytes.Repeat([]byte{'x'}, 256*1024+1)), target); ok {
		t.Fatal("fetchLogoAsDataURL() accepted oversized image")
	}
	dataURL, ok := fetchLogoAsDataURL(response("image/png", []byte("png")), target)
	if !ok || !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("fetchLogoAsDataURL() = %q, %v", dataURL, ok)
	}
}

func TestFetchLogoRejectsNonWebPortBeforeRequest(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	if _, ok := fetchLogoAsDataURL(client, mustParseURL(t, "https://example.com:22/icon.png")); ok {
		t.Fatal("fetchLogoAsDataURL() accepted a non-Web port")
	}
	if called {
		t.Fatal("fetchLogoAsDataURL() sent a request to a rejected port")
	}
}

func TestDiscoverSiteLogoCandidatesIsBounded(t *testing.T) {
	var html strings.Builder
	for index := 0; index < maxSiteLogoCandidates+20; index++ {
		html.WriteString(`<link rel="icon" href="/icon-`)
		html.WriteString(string(rune('a' + index)))
		html.WriteString(`.png">`)
	}
	candidates := discoverSiteLogoCandidates(mustParseURL(t, "https://example.com/"), []byte(html.String()))
	if len(candidates) != maxSiteLogoCandidates {
		t.Fatalf("candidate count = %d, want %d", len(candidates), maxSiteLogoCandidates)
	}
}

func TestFetchLogoStopsBeforeRequestWhenContextCanceled(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, context.Canceled
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := fetchLogoAsDataURLContext(ctx, client, mustParseURL(t, "https://example.com/icon.png")); ok {
		t.Fatal("fetchLogoAsDataURLContext() succeeded with a canceled context")
	}
	if called {
		t.Fatal("fetchLogoAsDataURLContext() dispatched a request after cancellation")
	}
}

func TestSiteLogoCacheIsBounded(t *testing.T) {
	server := NewServer("test", model.DefaultConfig())
	for index := 0; index < maxSiteLogoCacheEntries+20; index++ {
		server.storeSiteLogoCache(string(rune(index+1)), siteLogoResponse{OK: true})
	}
	server.siteLogoMu.RLock()
	count := len(server.siteLogoCache)
	server.siteLogoMu.RUnlock()
	if count > maxSiteLogoCacheEntries {
		t.Fatalf("site logo cache size = %d, want <= %d", count, maxSiteLogoCacheEntries)
	}

	server.siteLogoMu.Lock()
	server.siteLogoCache["expired"] = siteLogoCacheEntry{ExpiresAt: time.Now().Add(-time.Minute)}
	server.siteLogoMu.Unlock()
	server.storeSiteLogoCache("replacement", siteLogoResponse{OK: true})
	if _, ok := server.lookupSiteLogoCache("expired"); ok {
		t.Fatal("expired site logo cache entry was retained")
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
