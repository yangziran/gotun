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
)

// LocalTunnel 实现了本地端口转发隧道 (类似 ssh -L)
type LocalTunnel struct {
	cfg  config.TunnelConfig
	pool *pool.Pool

	ln      net.Listener // 本地持久化监听器
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
	mu      sync.Mutex

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

// Start 启动隧道：建立本地监听，并在后台派生请求接收协程
func (t *LocalTunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("tunnel %s is already running", t.cfg.Name)
	}
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.running = true
	t.mu.Unlock()

	logger.Info("正在启动本地转发隧道...", "tunnel", t.cfg.Name)

	ln, err := net.Listen("tcp", t.cfg.LocalAddr)
	if err != nil {
		return fmt.Errorf("listen on local address %s failed: %w", t.cfg.LocalAddr, err)
	}
	t.ln = ln
	logger.Info("本地端口正在监听，将转发至远端", "tunnel", t.cfg.Name, "local", t.cfg.LocalAddr, "remote", t.cfg.RemoteAddr, "servers", t.cfg.ServerNames)

	t.wg.Add(1)
	go t.acceptLoop()

	return nil
}

// acceptLoop 接收本地连接
func (t *LocalTunnel) acceptLoop() {
	defer t.wg.Done()
	for {
		localConn, err := t.ln.Accept()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			default:
				logger.Error("接收本地连接失败", "tunnel", t.cfg.Name, "err", err)
				return
			}
		}

		t.wg.Add(1)
		go t.handleConnection(localConn)
	}
}

// handleConnection 负责建立端到端的数据流转发 (含轮询负载均衡与指标统计)
func (t *LocalTunnel) handleConnection(localConn net.Conn) {
	defer t.wg.Done()
	defer localConn.Close()

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

	client, err := t.pool.GetReadyClient(t.ctx, targetServer)
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
		buf := getBuffer()
		defer putBuffer(buf)
		tw := metrics.NewTrackingWriter(remoteConn, t.cfg.Name, "up")
		_, _ = io.CopyBuffer(tw, localConn, buf)
		remoteConn.Close() // 上行结束，切断远端以释放下行协程
	}()

	go func() {
		defer ioWg.Done()
		buf := getBuffer()
		defer putBuffer(buf)
		tw := metrics.NewTrackingWriter(localConn, t.cfg.Name, "down")
		_, _ = io.CopyBuffer(tw, remoteConn, buf)
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
	t.mu.Unlock()

	logger.Info("正在停止隧道...", "tunnel", t.cfg.Name)
	if t.ln != nil {
		t.ln.Close()
	}

	go func() {
		t.wg.Wait()
		logger.Info("隧道已完全停止（旧连接已排空）。", "tunnel", t.cfg.Name)
	}()
}
