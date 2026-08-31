package router

import (
	"context"
	"net/netip"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/pkg/logger"
)

// Router 负责决定流量是走代理还是直连
type Router struct {
	cfg       config.RoutingConfig
	chnroutes *Chnroutes
}

// NewRouter 创建一个新的路由引擎
func NewRouter(ctx context.Context, cfg config.RoutingConfig) *Router {
	r := &Router{
		cfg: cfg,
	}

	// 默认设置
	if r.cfg.Mode == "" {
		r.cfg.Mode = "global"
	}
	if r.cfg.Mode == "pac" && r.cfg.ChnroutesURL == "" {
		r.cfg.ChnroutesURL = "https://cdn.jsdelivr.net/gh/misakaio/chnroutes2@master/chnroutes.txt"
	}

	if r.cfg.Mode == "pac" {
		r.chnroutes = NewChnroutes()
		// 异步加载 chnroutes，避免阻塞启动
		go func() {
			if err := r.chnroutes.Load(r.cfg.ChnroutesURL); err != nil {
				logger.Error("Failed to load chnroutes", "err", err)
			} else {
				logger.Info("Successfully loaded chnroutes")
			}
		}()
	}

	logger.Info("Router initialized", "mode", r.cfg.Mode, "bypass_lan", r.cfg.BypassLAN)
	return r
}

// ShouldProxy 返回 true 表示走代理，false 表示直连
func (r *Router) ShouldProxy(host string) bool {
	if r.cfg.Mode == "direct" {
		return false
	}

	// 解析是否为 IP
	ip, err := netip.ParseAddr(host)
	isIP := err == nil

	if isIP && r.cfg.BypassLAN && ip.IsPrivate() {
		logger.Debug("Bypass LAN IP", "ip", ip.String())
		return false
	}
	if isIP && r.cfg.BypassLAN && ip.IsLoopback() {
		logger.Debug("Bypass Loopback IP", "ip", ip.String())
		return false
	}

	if r.cfg.Mode == "pac" {
		if isIP {
			// 如果是国内 IP，则直连
			if r.chnroutes != nil && r.chnroutes.Contains(ip) {
				logger.Debug("Bypass domestic IP", "ip", ip.String())
				return false
			}
			// 否则走代理
			return true
		} else {
			// 如果是域名，简单的 pac 逻辑：默认走代理。
			return true
		}
	}

	return true
}
