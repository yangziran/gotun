package manager

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/internal/pool"
	"github.com/yangziran/gotun/internal/tunnel"
	"github.com/yangziran/gotun/pkg/logger"
)

// Manager 是隧道调度器，负责管理全局 Pool 以及所有隧道的生命周期
type Manager struct {
	mu            sync.Mutex
	cfg           *config.Config
	tunnels       map[string]tunnel.Tunnel
	tunnelConfigs map[string]config.TunnelConfig
	pool          *pool.Pool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewManager 创建一个隧道管理器实例
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:           cfg,
		tunnels:       make(map[string]tunnel.Tunnel),
		tunnelConfigs: make(map[string]config.TunnelConfig),
	}
}

// Start 启动全局 Pool 和所有的隧道实例，并启动 Metrics
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.pool = pool.NewPool(m.ctx)

	// 启动 Metrics (内部保证了 sync.Once，重复启动不会报错)
	metrics.StartMetricsServer(m.cfg.MetricsAddr)

	m.pool.UpdateServers(m.cfg.Servers)
	m.startTunnels(m.cfg.Tunnels)

	return nil
}

// startTunnels 启动指定的隧道列表 (非线程安全，调用方需加锁)
func (m *Manager) startTunnels(tunnelsCfg []config.TunnelConfig) {
	for _, tunCfg := range tunnelsCfg {
		if _, exists := m.tunnels[tunCfg.Name]; exists {
			continue // 已经运行的同名隧道忽略
		}

		var t tunnel.Tunnel
		if tunCfg.Type == "local" {
			t = tunnel.NewLocalTunnel(tunCfg, m.pool)
		} else if tunCfg.Type == "dynamic" {
			t = tunnel.NewDynamicTunnel(tunCfg, m.pool)
		} else if tunCfg.Type == "remote" {
			t = tunnel.NewRemoteTunnel(tunCfg, m.pool)
		} else {
			logger.Warn("不支持的隧道类型", "type", tunCfg.Type, "tunnel", tunCfg.Name)
			continue
		}

		m.tunnels[tunCfg.Name] = t
		m.tunnelConfigs[tunCfg.Name] = tunCfg

		go func(tun tunnel.Tunnel, name string) {
			err := tun.Start(m.ctx)
			if err != nil {
				logger.Error("隧道启动失败", "tunnel", name, "err", err)
			}
		}(t, tunCfg.Name)
	}
	logger.Info(fmt.Sprintf("当前正在运行 %d 条隧道", len(m.tunnels)))
}

// Reload 实现零停机热加载
func (m *Manager) Reload(newCfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.Info("正在执行配置热加载 (Hot Reload)...")

	m.cfg = newCfg
	metrics.StartMetricsServer(m.cfg.MetricsAddr)

	// 1. 更新全局 Pool (自动复用未改变的连接，剔除被删除或已修改的连接)
	m.pool.UpdateServers(m.cfg.Servers)

	// 2. 计算需要保留的新隧道 Map
	newTunnelsMap := make(map[string]config.TunnelConfig)
	for _, t := range m.cfg.Tunnels {
		newTunnelsMap[t.Name] = t
	}

	// 3. 比对并更新隧道
	for name, t := range m.tunnels {
		newTunCfg, exists := newTunnelsMap[name]
		if !exists {
			logger.Info("停止并移除已弃用的隧道", "tunnel", name)
			t.Stop()
			delete(m.tunnels, name)
			delete(m.tunnelConfigs, name)
		} else {
			// 如果配置发生了改变，则停止旧的，让后续的 startTunnels 重新拉起
			oldTunCfg := m.tunnelConfigs[name]
			// 简单的比对：如果各项核心参数不一致，则重启
			if oldTunCfg.LocalAddr != newTunCfg.LocalAddr ||
				oldTunCfg.RemoteAddr != newTunCfg.RemoteAddr ||
				oldTunCfg.Type != newTunCfg.Type ||
				!reflect.DeepEqual(oldTunCfg.ServerNames, newTunCfg.ServerNames) {

				logger.Info("隧道配置已变更，准备重启该隧道", "tunnel", name)
				t.Stop()
				delete(m.tunnels, name)
				delete(m.tunnelConfigs, name)
			}
		}
	}

	// 4. 启动新加入的隧道
	m.startTunnels(m.cfg.Tunnels)

	logger.Info("配置热加载完成。")
}

// Stop 优雅关闭所有正在运行的隧道及连接池，并释放资源
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.Info("正在停止所有隧道和连接池...")

	for name, t := range m.tunnels {
		t.Stop()
		delete(m.tunnels, name)
	}

	if m.pool != nil {
		m.pool.Stop()
	}

	if m.cancel != nil {
		m.cancel()
	}

	logger.Info("所有系统组件已停止。")
}
