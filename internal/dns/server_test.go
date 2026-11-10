package dns

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/yangziran/gotun/internal/config"
)

// 缓存写入量超过上限后，缓存大小不得无限增长
func TestCacheUpperBound(t *testing.T) {
	s, err := NewServer(config.DNSConfig{Upstream: "udp://127.0.0.1:53"}, nil)
	if err != nil {
		t.Fatalf("创建 DNS Server 失败: %v", err)
	}
	defer s.Stop()

	entry := cacheEntry{
		msg:       new(dns.Msg),
		expiresAt: time.Now().Add(time.Hour), // 远未过期，强制触发容量淘汰
	}
	for i := 0; i < maxCacheEntries+500; i++ {
		s.storeCache(fmt.Sprintf("domain%d.example.|1|1", i), entry)
	}

	s.cacheMu.RLock()
	n := len(s.cache)
	s.cacheMu.RUnlock()

	if n > maxCacheEntries {
		t.Fatalf("缓存条目数 %d 超过上限 %d", n, maxCacheEntries)
	}
}

// 过期条目会被清扫，存活条目保留
func TestPurgeExpired(t *testing.T) {
	s, err := NewServer(config.DNSConfig{Upstream: "udp://127.0.0.1:53"}, nil)
	if err != nil {
		t.Fatalf("创建 DNS Server 失败: %v", err)
	}
	defer s.Stop()

	now := time.Now()
	s.storeCache("expired.example.|1|1", cacheEntry{msg: new(dns.Msg), expiresAt: now.Add(-time.Second)})
	s.storeCache("alive.example.|1|1", cacheEntry{msg: new(dns.Msg), expiresAt: now.Add(time.Hour)})

	s.cacheMu.Lock()
	s.purgeExpiredLocked()
	s.cacheMu.Unlock()

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if _, ok := s.cache["expired.example.|1|1"]; ok {
		t.Fatal("过期条目未被清扫")
	}
	if _, ok := s.cache["alive.example.|1|1"]; !ok {
		t.Fatal("未过期条目被误删")
	}
}
