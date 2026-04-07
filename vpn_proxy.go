package fastlike

import "net/http"

// VpnProxyInfo contains VPN/proxy intelligence data for a downstream request's IP address.
// Nil pointer fields indicate the value is not available (maps to FastlyStatus::NONE).
type VpnProxyInfo struct {
	IsAnonymous        *bool
	IsAnonymousVPN     *bool
	IsHostingProvider  *bool
	IsProxyOverVPN     *bool
	IsPublicProxy      *bool
	IsRelayProxy       *bool
	IsResidentialProxy *bool
	IsSmartDNSProxy    *bool
	IsTorExitNode      *bool
	IsVPNDatacenter    *bool
	ServiceName        string
}

// VpnProxyFunc is a function that returns VPN/proxy intelligence for a request.
// Return nil for no VPN proxy data (all hostcalls will return NONE).
type VpnProxyFunc func(*http.Request) *VpnProxyInfo
