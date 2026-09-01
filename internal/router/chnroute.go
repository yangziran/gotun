package router

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/yangziran/gotun/pkg/logger"
)

// Chnroutes 维护国内 IP 段列表，支持快速查询
type Chnroutes struct {
	mu       sync.RWMutex
	prefixes []netip.Prefix
}

// NewChnroutes 实例化并返回一个并发安全的国内 IP 分流路由表引擎
func NewChnroutes() *Chnroutes {
	return &Chnroutes{
		prefixes: make([]netip.Prefix, 0),
	}
}

// Load 从指定 URL 下载并解析 IP 段
func (c *Chnroutes) Load(url string) error {
	logger.Info("Downloading chnroutes...", "url", url)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to GET url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return c.parse(resp.Body)
}

// parse 解析包含 CIDR 的文本流，忽略注释
func (c *Chnroutes) parse(r io.Reader) error {
	var newPrefixes []netip.Prefix
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			logger.Warn("Failed to parse prefix in chnroutes", "line", line, "err", err)
			continue
		}
		newPrefixes = append(newPrefixes, prefix)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading chnroutes data: %w", err)
	}

	c.mu.Lock()
	c.prefixes = newPrefixes
	c.mu.Unlock()

	logger.Info("Parsed chnroutes", "count", len(newPrefixes))
	return nil
}

// Contains 检查给定 IP 是否属于国内 IP 段
func (c *Chnroutes) Contains(ip netip.Addr) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 线性遍历，大约8000个元素，微秒级开销
	for _, prefix := range c.prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
