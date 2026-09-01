package fetcher

import (
	"net"
	"net/netip"
	"strings"
)

var blockedPublicNetworkPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2002::/16"),
}

func isBlockedHostname(host string) bool {
	value := strings.ToLower(strings.TrimSpace(host))
	return value == "localhost" || strings.HasSuffix(value, ".local")
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPublicNetworkPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// IsPublicIP reports whether an address is safe for public-mode outbound
// HTTP requests. Callers must still pin the validated address when dialing.
func IsPublicIP(ip net.IP) bool {
	return !isBlockedIP(ip)
}
