package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/internal/pool"
	"github.com/yangziran/gotun/pkg/logger"
	"github.com/yangziran/gotun/pkg/utils"
)

// LocalTunnel 实现了本地端口转发隧道 (类似 ssh -L)
type LocalTunnel struct {
	cfg  config.TunnelConfig
	pool *pool.Pool

	listeners []net.Listener // 本地持久化监听器列表
	tracker   connTracker    // 活动连接跟踪与 Stop/Start 排空协调
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	mu        sync.Mutex

	robinIndex uint64
}

// NewLocalTunnel 创建一个新的 Local 转发隧道实例
func NewLocalTunnel(cfg config.TunnelConfig, p *pool.Pool) *LocalTunnel {
	return &LocalTunnel{
		cfg:  cfg,
		pool: p,
	}
}

// GetName 返回隧道名称
func (t *LocalTunnel) GetName() string {
	return t.cfg.Name
}

// Start 启动隧道：同步完成本地监听绑定，成功后在后台派生请求接收协程
func (t *LocalTunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return fmt.Errorf("tunnel %s is already running", t.cfg.Name)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tunnel %s start aborted: %w", t.cfg.Name, err)
	}

	// 等待上一代 Stop 的排空完成，避免 wg.Add 与上一代 wg.Wait 并发
	if ch := t.tracker.start(); ch != nil {
		<-ch
	}

	t.ctx, t.cancel = context.WithCancel(ctx)
	t.running = true

	logger.Info("正在启动本地转发隧道...", "tunnel", t.cfg.Name)

	addrs := utils.GetDualStackAddrs(t.cfg.LocalAddr)
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			logger.Warn("双栈监听失败 (可能不支持IPv6，将忽略)", "tunnel", t.cfg.Name, "addr", addr, "err", err)
			continue
		}
		t.listeners = append(t.listeners, ln)
		logger.Info("本地端口正在监听，将转发至远端", "tunnel", t.cfg.Name, "local", addr, "remote", t.cfg.RemoteAddr, "servers", t.cfg.ServerNames)
	}

	// 绑定期间若父上下文已被取消（例如 Stop 已调用），立即清理监听器，防止泄漏
	if err := t.ctx.Err(); err != nil || len(t.listeners) == 0 {
		if len(t.listeners) == 0 {
			err = fmt.Errorf("listen on local addresses failed for tunnel %s", t.cfg.Name)
		}
		for _, ln := range t.listeners {
			ln.Close()
		}
		t.listeners = nil
		t.running = false
		t.cancel()
		t.cancel = nil
		return err
	}

	tunCtx := t.ctx
	for _, ln := range t.listeners {
		t.wg.Add(1)
		go t.acceptLoop(tunCtx, ln)
	}

	return nil
}

// acceptLoop 接收本地连接
func (t *LocalTunnel) acceptLoop(ctx context.Context, ln net.Listener) {
	defer t.wg.Done()
	for {
		localConn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				logger.Error("接收本地连接失败", "tunnel", t.cfg.Name, "err", err)
				return
			}
		}

		t.wg.Add(1)
		go t.handleConnection(ctx, localConn)
	}
}

// handleConnection 负责建立端到端的数据流转发 (含轮询负载均衡与指标统计)
func (t *LocalTunnel) handleConnection(ctx context.Context, localConn net.Conn) {
	defer t.wg.Done()
	defer localConn.Close()

	// 隧道已停止时直接关闭新接入的连接，避免 Stop 后仍转发数据
	if !t.tracker.add(localConn) {
		return
	}
	defer t.tracker.remove(localConn)

	if len(t.cfg.ServerNames) == 0 {
		logger.Error("拒绝连接：未配置 ServerNames", "tunnel", t.cfg.Name)
		return
	}

	metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Inc()
	defer metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Dec()

	// 轮询选取 Server
	idx := atomic.AddUint64(&t.robinIndex, 1)
	targetServer := t.cfg.ServerNames[idx%uint64(len(t.cfg.ServerNames))]

	logger.Debug("已接收新的本地连接，准备向 Server 索取连接", "tunnel", t.cfg.Name, "client", localConn.RemoteAddr(), "dispatch_to", targetServer)

	// 限制等待节点就绪的时间，避免在无响应节点上无限阻塞
	waitCtx, waitCancel := context.WithTimeout(ctx, pool.GetReadyTimeout)
	defer waitCancel()
	client, err := t.pool.GetReadyClient(waitCtx, targetServer)
	if err != nil {
		logger.Error("拒绝连接：无法从池中获取 SSH 客户端", "tunnel", t.cfg.Name, "server", targetServer, "err", err)
		return
	}

	remoteConn, err := client.Dial("tcp", t.cfg.RemoteAddr)
	if err != nil {
		logger.Error("无法拨号到远端目标", "tunnel", t.cfg.Name, "target", t.cfg.RemoteAddr, "err", err)
		return
	}
	defer remoteConn.Close()

	var ioWg sync.WaitGroup
	ioWg.Add(2)

	go func() {
		defer ioWg.Done()
		bufPtr := getBuffer()
		defer putBuffer(bufPtr)
		tw := metrics.NewTrackingWriter(remoteConn, t.cfg.Name, "up")
		_, _ = io.CopyBuffer(tw, localConn, *bufPtr)
		remoteConn.Close() // 上行结束，切断远端以释放下行协程
	}()

	go func() {
		defer ioWg.Done()
		bufPtr := getBuffer()
		defer putBuffer(bufPtr)
		tw := metrics.NewTrackingWriter(localConn, t.cfg.Name, "down")
		_, _ = io.CopyBuffer(tw, remoteConn, *bufPtr)
		localConn.Close() // 下行结束，切断本地以释放上行协程
	}()

	ioWg.Wait()
	logger.Debug("连接已关闭", "tunnel", t.cfg.Name, "client", localConn.RemoteAddr())
}

// Stop 终止并清理隧道资源
func (t *LocalTunnel) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	if t.cancel != nil {
		t.cancel()
	}
	listeners := t.listeners
	t.listeners = nil
	t.mu.Unlock()

	logger.Info("正在停止隧道...", "tunnel", t.cfg.Name)

	// 先关闭监听器停止 accept，再断开所有活动连接，确保旧会话被终止
	for _, ln := range listeners {
		ln.Close()
	}
	t.tracker.stop()

	go func() {
		t.wg.Wait()
		t.tracker.drained()
		logger.Info("隧道已完全停止（旧连接已断开）。", "tunnel", t.cfg.Name)
	}()
}
