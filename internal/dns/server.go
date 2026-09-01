package dns

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/router"
	"github.com/yangziran/gotun/pkg/logger"
	"github.com/yangziran/gotun/pkg/utils"
)

// cacheEntry 用于 DNS 缓存
type cacheEntry struct {
	msg       *dns.Msg
	expiresAt time.Time
}

// Server 代表内置的 DNS 缓存服务器
type Server struct {
	cfg      config.DNSConfig
	router   *router.Router
	srvs     []*dns.Server
	resolver UpstreamResolver

	cache   map[string]cacheEntry
	cacheMu sync.RWMutex
}

// NewServer 创建 DNS 服务器
func NewServer(cfg config.DNSConfig, r *router.Router) *Server {
	if cfg.Upstream == "" {
		cfg.Upstream = "udp://119.29.29.29:53"
	}

	res, err := newResolver(cfg.Upstream)
	if err != nil {
		logger.Error("初始化 DNS 解析器失败，将回退到默认 UDP", "upstream", cfg.Upstream, "err", err)
		res = &udpResolver{addr: "119.29.29.29:53"}
	}

	return &Server{
		cfg:      cfg,
		router:   r,
		resolver: res,
		cache:    make(map[string]cacheEntry),
	}
}

// Start 启动 DNS 监听
func (s *Server) Start(ctx context.Context) error {
	if !s.cfg.Enable {
		return nil
	}
	if s.cfg.ListenAddr == "" {
		s.cfg.ListenAddr = "127.0.0.1:53"
	}

	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handleDNSRequest)

	addrs := utils.GetDualStackAddrs(s.cfg.ListenAddr)
	for _, addr := range addrs {
		srv := &dns.Server{
			Addr:    addr,
			Net:     "udp",
			Handler: mux,
		}
		s.srvs = append(s.srvs, srv)

		go func(address string, server *dns.Server) {
			logger.Info("启动 DNS 缓存服务器", "addr", address, "upstream", s.cfg.Upstream)
			if err := server.ListenAndServe(); err != nil {
				logger.Error("DNS 服务器异常退出", "addr", address, "err", err)
			}
		}(addr, srv)
	}

	return nil
}

// handleDNSRequest 处理客户端发来的 DNS 查询
func (s *Server) handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		dns.HandleFailed(w, r)
		return
	}

	q := r.Question[0]
	// 组装缓存 Key (包含类型以防止 A 和 AAAA 污染)
	cacheKey := fmt.Sprintf("%s|%d|%d", q.Name, q.Qtype, q.Qclass)

	// 1. 查缓存
	s.cacheMu.RLock()
	entry, found := s.cache[cacheKey]
	s.cacheMu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		resp := entry.msg.Copy()
		resp.SetReply(r)
		_ = w.WriteMsg(resp)
		return
	}

	// 2. 向上游查询
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := s.resolver.Resolve(ctx, r)
	if err != nil {
		logger.Error("DNS 上游查询失败", "domain", q.Name, "upstream", s.cfg.Upstream, "err", err)
		dns.HandleFailed(w, r)
		return
	}

	// 3. 写入缓存并返回
	resp.SetReply(r)
	if len(resp.Answer) > 0 {
		minTTL := uint32(3600)
		for _, ans := range resp.Answer {
			if ans.Header().Ttl < minTTL {
				minTTL = ans.Header().Ttl
			}
		}

		if minTTL > 0 {
			s.cacheMu.Lock()
			s.cache[cacheKey] = cacheEntry{
				msg:       resp,
				expiresAt: time.Now().Add(time.Duration(minTTL) * time.Second),
			}
			s.cacheMu.Unlock()
		}
	}

	_ = w.WriteMsg(resp)
}

// Stop 停止 DNS 服务
func (s *Server) Stop() {
	if len(s.srvs) > 0 {
		logger.Info("正在停止 DNS 服务器...")
		for _, srv := range s.srvs {
			_ = srv.Shutdown()
		}
	}
}
