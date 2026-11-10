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

const (
	// maxCacheEntries 限制 DNS 缓存的最大条目数，防止无界膨胀
	maxCacheEntries = 1000
	// cacheCleanInterval 是后台清扫过期缓存条目的间隔
	cacheCleanInterval = 5 * time.Minute
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

	ctx    context.Context
	cancel context.CancelFunc

	cache   map[string]cacheEntry
	cacheMu sync.RWMutex
}

// NewServer 创建 DNS 服务器
func NewServer(cfg config.DNSConfig, r *router.Router) (*Server, error) {
	if cfg.Upstream == "" {
		cfg.Upstream = "udp://119.29.29.29:53"
	}

	res, err := newResolver(cfg.Upstream)
	if err != nil {
		// 不再静默回退到明文 UDP，避免隐私降级；由调用方决定失败策略
		return nil, fmt.Errorf("初始化 DNS 解析器失败 (upstream: %s): %w", cfg.Upstream, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		cfg:      cfg,
		router:   r,
		resolver: res,
		ctx:      ctx,
		cancel:   cancel,
		cache:    make(map[string]cacheEntry),
	}, nil
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

	// 后台定期清扫过期缓存条目
	go s.cacheCleaner()

	return nil
}

// storeCache 写入缓存条目；达到容量上限时先清扫过期项，仍满则迭代删除最早条目
func (s *Server) storeCache(key string, entry cacheEntry) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if len(s.cache) >= maxCacheEntries {
		s.purgeExpiredLocked()
	}
	for len(s.cache) >= maxCacheEntries {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}

	s.cache[key] = entry
}

// purgeExpiredLocked 删除所有已过期的缓存条目（调用方需持有写锁）
func (s *Server) purgeExpiredLocked() {
	now := time.Now()
	for k, v := range s.cache {
		if !now.Before(v.expiresAt) {
			delete(s.cache, k)
		}
	}
}

// cacheCleaner 每 5 分钟清扫一次过期缓存，随 Server 停止而退出
func (s *Server) cacheCleaner() {
	ticker := time.NewTicker(cacheCleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cacheMu.Lock()
			s.purgeExpiredLocked()
			s.cacheMu.Unlock()
		}
	}
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
			s.storeCache(cacheKey, cacheEntry{
				msg:       resp,
				expiresAt: time.Now().Add(time.Duration(minTTL) * time.Second),
			})
		}
	}

	_ = w.WriteMsg(resp)
}

// Stop 停止 DNS 服务
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if len(s.srvs) > 0 {
		logger.Info("正在停止 DNS 服务器...")
		for _, srv := range s.srvs {
			_ = srv.Shutdown()
		}
	}
}
