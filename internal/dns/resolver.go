package dns

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/tjfoc/gmsm/gmtls"
	gmx509 "github.com/tjfoc/gmsm/x509"
)

// UpstreamResolver 定义了 DNS 向上游请求解析的统一接口规范。
// 采用了策略模式 (Strategy Pattern)，允许动态切换 UDP/DoT/DoH/国密 等底层实现。
type UpstreamResolver interface {
	Resolve(ctx context.Context, r *dns.Msg) (*dns.Msg, error)
}

// newResolver 工厂函数：根据 upstream 前缀分发对应的解析器
func newResolver(upstream string) (UpstreamResolver, error) {
	if strings.HasPrefix(upstream, "tls://") {
		return &dotResolver{addr: strings.TrimPrefix(upstream, "tls://")}, nil
	} else if strings.HasPrefix(upstream, "https://") {
		return &dohResolver{
			url:    upstream,
			client: &http.Client{Timeout: 5 * time.Second},
		}, nil
	} else if strings.HasPrefix(upstream, "gmdoh://") {
		return newGmDohResolver(strings.Replace(upstream, "gmdoh://", "https://", 1))
	} else if strings.HasPrefix(upstream, "udp://") {
		return &udpResolver{addr: strings.TrimPrefix(upstream, "udp://")}, nil
	}
	// 默认 UDP
	return &udpResolver{addr: upstream}, nil
}

// udpResolver UDP 标准解析
type udpResolver struct {
	addr string
}

func (u *udpResolver) Resolve(ctx context.Context, r *dns.Msg) (*dns.Msg, error) {
	client := new(dns.Client)
	client.Timeout = 3 * time.Second
	resp, _, err := client.Exchange(r, u.addr)
	return resp, err
}

// dotResolver DoT (DNS over TLS) 解析
type dotResolver struct {
	addr string
}

func (d *dotResolver) Resolve(ctx context.Context, r *dns.Msg) (*dns.Msg, error) {
	client := new(dns.Client)
	client.Net = "tcp-tls"
	client.Timeout = 3 * time.Second
	resp, _, err := client.Exchange(r, d.addr)
	return resp, err
}

// dohResolver DoH (DNS over HTTPS) 解析
type dohResolver struct {
	url    string
	client *http.Client
}

func (h *dohResolver) Resolve(ctx context.Context, r *dns.Msg) (*dns.Msg, error) {
	msgBytes, err := r.Pack()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(msgBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	dnsResp := new(dns.Msg)
	if err := dnsResp.Unpack(body); err != nil {
		return nil, err
	}

	return dnsResp, nil
}

// gmDohResolver 国密 DoH (TLCP) 解析
type gmDohResolver struct {
	url    string
	client *http.Client
}

// newGmDohResolver 创建国密 DoH 解析器。
// TLCP 握手由 gmsm/gmtls 完成，但证书链与主机名校验委托给标准库 crypto/x509：
// gmsm/x509 只能从 Linux 文件路径加载系统根（macOS/Windows 上返回空池），
// 而标准库系统根跨平台可靠（macOS 走平台校验器，实测见 PLAN.md 阶段 B 记录）。
// 任何校验失败都会拒绝连接，不存在静默跳过验证的路径。
func newGmDohResolver(url string) (*gmDohResolver, error) {
	stdRoots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("国密 DoH: 加载系统根证书失败: %w", err)
	}
	if stdRoots == nil {
		return nil, fmt.Errorf("国密 DoH: 系统根证书不可用")
	}

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout(network, addr, 3*time.Second)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			tlsConn := gmtls.Client(conn, &gmtls.Config{
				ServerName: host,
				// 仅跳过 gmsm 内置校验（其系统根加载不支持 macOS/Windows）；
				// 真实证书链与主机名校验由 VerifyPeerCertificate 执行。
				InsecureSkipVerify: true,
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*gmx509.Certificate) error {
					return verifyTLCPCerts(rawCerts, host, stdRoots)
				},
			})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}

	return &gmDohResolver{
		url: url,
		client: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
	}, nil
}

// verifyTLCPCerts 用标准库校验 TLCP 对端证书链与主机名（SM2/ECC/RSA 证书均可）。
func verifyTLCPCerts(rawCerts [][]byte, dnsName string, roots *x509.CertPool) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("国密 DoH: 对端未提供证书")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("国密 DoH: 解析对端证书失败: %w", err)
	}
	intermediates := x509.NewCertPool()
	for _, raw := range rawCerts[1:] {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("国密 DoH: 解析中间证书失败: %w", err)
		}
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: dnsName, Roots: roots, Intermediates: intermediates}); err != nil {
		return fmt.Errorf("国密 DoH: 证书校验失败: %w", err)
	}
	return nil
}

func (h *gmDohResolver) Resolve(ctx context.Context, r *dns.Msg) (*dns.Msg, error) {
	msgBytes, err := r.Pack()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(msgBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gm-doh upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	dnsResp := new(dns.Msg)
	if err := dnsResp.Unpack(body); err != nil {
		return nil, err
	}

	return dnsResp, nil
}
