package risk

import (
	"context"
	"fmt"
	"net"
	"strings"
)

type IPWhitelistRule struct {
	exactIPs []net.IP
	cidrs    []*net.IPNet
}

func NewIPWhitelistRule(entries []string) (*IPWhitelistRule, error) {
	r := &IPWhitelistRule{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, n, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid cidr %q: %w", raw, err)
			}
			r.cidrs = append(r.cidrs, n)
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip %q", raw)
		}
		r.exactIPs = append(r.exactIPs, ip)
	}
	return r, nil
}

func (r *IPWhitelistRule) Name() string { return "ip_whitelist" }

func (r *IPWhitelistRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.ClientIP == nil {
		return Deny("client ip missing"), nil
	}
	for _, ip := range r.exactIPs {
		if ip.Equal(in.ClientIP) {
			return Allow(), nil
		}
	}
	for _, c := range r.cidrs {
		if c.Contains(in.ClientIP) {
			return Allow(), nil
		}
	}
	return Deny(fmt.Sprintf("ip %s not in whitelist", in.ClientIP)), nil
}
