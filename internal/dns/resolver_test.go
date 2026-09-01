package dns

import (
	"context"
	"testing"

	"github.com/miekg/dns"
)

func createDNSQuery(domain string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = true
	return m
}

func TestResolvers(t *testing.T) {
	tests := []struct {
		name     string
		upstream string
	}{
		{"UDP Resolver", "udp://119.29.29.29:53"},
		{"DoT Resolver", "tls://dot.pub:853"},
		{"DoH Resolver", "https://doh.pub/dns-query"},
		{"GM-DoH Resolver", "gmdoh://sm2.doh.pub/dns-query"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, err := newResolver(tt.upstream)
			if err != nil {
				t.Fatalf("Failed to create resolver %s: %v", tt.upstream, err)
			}

			req := createDNSQuery("www.qq.com", dns.TypeA)
			resp, err := resolver.Resolve(context.Background(), req)
			if err != nil {
				t.Fatalf("Resolution failed for %s: %v", tt.upstream, err)
			}

			if len(resp.Answer) == 0 {
				t.Errorf("Expected answers, got 0 for %s", tt.upstream)
			}
		})
	}
}
