package dns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// 生成一张自签名 ECDSA 测试证书（离线，不联网）
func makeSelfSignedCert(t *testing.T, dnsName string) (*x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发证书失败: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析证书失败: %v", err)
	}
	return cert, der
}

// 阶段 B2 回归测试：TLCP 证书校验必须是真实校验（链 + 主机名），不得静默放行。
func TestVerifyTLCPCerts(t *testing.T) {
	cert, der := makeSelfSignedCert(t, "sm2.doh.pub")

	trustingRoots := x509.NewCertPool()
	trustingRoots.AddCert(cert)

	t.Run("信任根内且主机名匹配则通过", func(t *testing.T) {
		if err := verifyTLCPCerts([][]byte{der}, "sm2.doh.pub", trustingRoots); err != nil {
			t.Fatalf("期望通过，实际: %v", err)
		}
	})

	t.Run("主机名不匹配则拒绝", func(t *testing.T) {
		if err := verifyTLCPCerts([][]byte{der}, "evil.example.com", trustingRoots); err == nil {
			t.Fatal("期望因主机名不匹配被拒绝，实际放行")
		}
	})

	t.Run("不在信任根内则拒绝", func(t *testing.T) {
		if err := verifyTLCPCerts([][]byte{der}, "sm2.doh.pub", x509.NewCertPool()); err == nil {
			t.Fatal("期望因不受信任被拒绝，实际放行")
		}
	})

	t.Run("对端未提供证书则拒绝", func(t *testing.T) {
		if err := verifyTLCPCerts(nil, "sm2.doh.pub", trustingRoots); err == nil {
			t.Fatal("期望因无证书被拒绝，实际放行")
		}
	})
}

func createDNSQuery(domain string, qtype uint16) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = true
	return m
}

func TestResolvers(t *testing.T) {
	// 依赖真实公网上游（国内 DoH/DoT/国密服务），CI 海外 runner 不可达。
	// 默认跳过；本地验证连通性时显式开启：GOTUN_E2E=1 go test ./internal/dns/
	if os.Getenv("GOTUN_E2E") == "" {
		t.Skip("跳过公网上游连通性测试（设置 GOTUN_E2E=1 启用）")
	}
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
