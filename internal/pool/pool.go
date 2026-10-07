package pool

import (
	"context"
	"fmt"
	"sync"

	"github.com/yangziran/gotun/internal/config"
	"golang.org/x/crypto/ssh"
)

type Pool struct {
	mu      sync.RWMutex
	servers map[string]*Client
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewPool(ctx context.Context) *Pool {
	p := &Pool{
		servers: make(map[string]*Client),
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	return p
}

// UpdateServers 动态增删改物理服务器连接池
func (p *Pool) UpdateServers(configs []config.ServerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	newMap := make(map[string]*Client)
	for _, cfg := range configs {
		existing, ok := p.servers[cfg.Name]
		if ok && isServerConfigEqual(existing.srvCfg, cfg) {
			// 配置没有变化，复用旧 Client
			newMap[cfg.Name] = existing
			delete(p.servers, cfg.Name)
		} else {
			if ok {
				// 配置有变化，需要停止旧的
				existing.Stop()
				delete(p.servers, cfg.Name)
			}
			newMap[cfg.Name] = newClient(p.ctx, cfg, p)
		}
	}

	// 停止被移除的 Server
	for _, c := range p.servers {
		c.Stop()
	}

	p.servers = newMap
}

// GetReadyClient 获取指定服务器的可用 SSH 客户端。如果没连接好，将阻塞等待。
func (p *Pool) GetReadyClient(ctx context.Context, name string) (*ssh.Client, error) {
	p.mu.RLock()
	c, ok := p.servers[name]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server not found in pool: %s", name)
	}

	return c.GetSSHClient(ctx)
}

func (p *Pool) Stop() {
	p.cancel()
	p.mu.Lock()
	for _, c := range p.servers {
		c.Stop()
	}
	p.mu.Unlock()
}

func isServerConfigEqual(a, b config.ServerConfig) bool {
	return a.Name == b.Name &&
		a.Host == b.Host &&
		a.Port == b.Port &&
		a.User == b.User &&
		a.Password == b.Password &&
		a.Encrypted == b.Encrypted &&
		a.KeyPath == b.KeyPath &&
		a.JumpHost == b.JumpHost
}
