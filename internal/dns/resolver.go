package dns

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/tjfoc/gmsm/gmtls"
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

func newGmDohResolver(url string) (*gmDohResolver, error) {
	gmConfig := &gmtls.Config{
		InsecureSkipVerify: true, // 默认跳过证书校验，若需严格校验可配置根证书池
	}

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout(network, addr, 3*time.Second)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			cfg := gmConfig.Clone()
			cfg.ServerName = host
			tlsConn := gmtls.Client(conn, cfg)
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
