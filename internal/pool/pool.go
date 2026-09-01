package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/yangziran/gotun/internal/config"
	"golang.org/x/crypto/ssh"
)

// ServerGroup 维护了通向同一个目标宿主机的多条并发 SSH 连接 (Client)。
// 用于解决跨国高延迟(LFN)情况下的单通道 TCP 队头阻塞问题。
type ServerGroup struct {
	cfg     config.ServerConfig
	clients []*Client
	next    uint32
}

func newServerGroup(ctx context.Context, cfg config.ServerConfig, p *Pool) *ServerGroup {
	size := cfg.PoolSize
	if size <= 0 {
		size = 1 // default to 1 connection
	}
	sg := &ServerGroup{
		cfg:     cfg,
		clients: make([]*Client, size),
	}
	for i := 0; i < size; i++ {
		sg.clients[i] = newClient(ctx, cfg, p)
	}
	return sg
}

func (sg *ServerGroup) Stop() {
	for _, c := range sg.clients {
		c.Stop()
	}
}

func (sg *ServerGroup) GetSSHClient(ctx context.Context) (*ssh.Client, error) {
	idx := atomic.AddUint32(&sg.next, 1) % uint32(len(sg.clients))
	return sg.clients[idx].GetSSHClient(ctx)
}

// Pool 是全局隧道连接池核心组件，负责集中管理、调度与复用所有节点的物理 SSH 链路。
// 它解耦了业务隧道与底层网络，实现了多隧道对底层连接的高效复用。
type Pool struct {
	mu      sync.RWMutex
	servers map[string]*ServerGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewPool 创建并初始化一个受 Context 生命周期控制的全局 SSH 连接池。
func NewPool(ctx context.Context) *Pool {
	p := &Pool{
		servers: make(map[string]*ServerGroup),
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	return p
}

// UpdateServers 动态增删改物理服务器连接池
func (p *Pool) UpdateServers(configs []config.ServerConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	newMap := make(map[string]*ServerGroup)
	for _, cfg := range configs {
		existing, ok := p.servers[cfg.Name]
		if ok && isServerConfigEqual(existing.cfg, cfg) {
			// 配置没有变化，复用旧 Group
			newMap[cfg.Name] = existing
			delete(p.servers, cfg.Name)
		} else {
			if ok {
				// 配置有变化，需要停止旧的
				existing.Stop()
				delete(p.servers, cfg.Name)
			}
			newMap[cfg.Name] = newServerGroup(p.ctx, cfg, p)
		}
	}

	// 停止被移除的 Server
	for _, sg := range p.servers {
		sg.Stop()
	}

	p.servers = newMap
}

// GetReadyClient 获取指定服务器的可用 SSH 客户端。如果没连接好，将阻塞等待。
func (p *Pool) GetReadyClient(ctx context.Context, name string) (*ssh.Client, error) {
	p.mu.RLock()
	sg, ok := p.servers[name]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server not found in pool: %s", name)
	}

	return sg.GetSSHClient(ctx)
}

func (p *Pool) Stop() {
	p.cancel()
	p.mu.Lock()
	for _, sg := range p.servers {
		sg.Stop()
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
		a.JumpHost == b.JumpHost &&
		a.PoolSize == b.PoolSize
}
