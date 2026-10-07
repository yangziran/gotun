package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/internal/pool"
	"github.com/yangziran/gotun/pkg/logger"
)

// RemoteTunnel 实现了远程反向端口转发隧道 (类似 ssh -R)
type RemoteTunnel struct {
	cfg  config.TunnelConfig
	pool *pool.Pool

	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
	mu      sync.Mutex

	robinIndex uint64
}

// NewRemoteTunnel 创建一个新的 Remote 远端反向隧道实例
func NewRemoteTunnel(cfg config.TunnelConfig, p *pool.Pool) *RemoteTunnel {
	return &RemoteTunnel{
		cfg:  cfg,
		pool: p,
	}
}

// GetName 返回隧道名称
func (t *RemoteTunnel) GetName() string {
	return t.cfg.Name
}

// Start 启动隧道：在后台派生远端监听协程
func (t *RemoteTunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("tunnel %s is already running", t.cfg.Name)
	}
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.running = true
	t.mu.Unlock()

	logger.Info("正在启动远端反向隧道...", "tunnel", t.cfg.Name)

	t.wg.Add(1)
	go t.maintainRemoteListener()

	return nil
}

// maintainRemoteListener 负责在可用的服务器上建立反向监听 (支持自动容灾Failover)
func (t *RemoteTunnel) maintainRemoteListener() {
	defer t.wg.Done()

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		if len(t.cfg.ServerNames) == 0 {
			logger.Error("拒绝监听：未配置 ServerNames", "tunnel", t.cfg.Name)
			return
		}

		// 轮询选取 Server 作为远端监听的主机
		idx := atomic.AddUint64(&t.robinIndex, 1)
		targetServer := t.cfg.ServerNames[idx%uint64(len(t.cfg.ServerNames))]

		logger.Debug("准备在服务器建立反向监听...", "tunnel", t.cfg.Name, "server", targetServer)

		// 阻塞获取可用的客户端
		client, err := t.pool.GetReadyClient(t.ctx, targetServer)
		if err != nil {
			logger.Error("获取 SSH 客户端失败，等待重试...", "tunnel", t.cfg.Name, "server", targetServer, "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		remoteLn, err := client.Listen("tcp", t.cfg.RemoteAddr)
		if err != nil {
			logger.Error("无法在远端地址建立监听 (可能未开启 GatewayPorts)，将尝试下一个节点", "tunnel", t.cfg.Name, "remote_addr", t.cfg.RemoteAddr, "err", err)
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Info("远端反向隧道已成功开启监听", "tunnel", t.cfg.Name, "server", targetServer, "remote_addr", t.cfg.RemoteAddr, "local_target", t.cfg.LocalAddr)

		// 为这单次监听分配特定的上下文
		sessionCtx, sessionCancel := context.WithCancel(t.ctx)

		go func() {
			<-sessionCtx.Done()
			remoteLn.Close()
		}()

		var acceptWg sync.WaitGroup
		acceptWg.Add(1)
		go t.remoteAcceptLoop(sessionCtx, remoteLn, &acceptWg)

		// 持续阻塞，直到底层 Client 断开或本隧道被关闭
		// 当物理连接断开时，remoteLn.Accept() 会返回错误并退出循环
		acceptWg.Wait()

		sessionCancel()
		remoteLn.Close()

		logger.Warn("远端监听意外终止，准备重试容灾节点...", "tunnel", t.cfg.Name)
		time.Sleep(2 * time.Second)
	}
}

// remoteAcceptLoop 是一个无限循环，负责接收从 SSH 远端发来的连接请求
func (t *RemoteTunnel) remoteAcceptLoop(ctx context.Context, ln net.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	// 跟踪当前会话的连接数
	var connWg sync.WaitGroup

	for {
		remoteConn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				connWg.Wait()
				return
			default:
				logger.Error("接收远端连接失败 (底层连接可能已断开)", "tunnel", t.cfg.Name, "err", err)
				connWg.Wait()
				return
			}
		}

		connWg.Add(1)
		go func(rConn net.Conn) {
			defer connWg.Done()
			t.handleConnection(rConn)
		}(remoteConn)
	}
}

// handleConnection 处理一条来自远端的连接，将其导向本地目标地址，并融入 Metrics
func (t *RemoteTunnel) handleConnection(remoteConn net.Conn) {
	defer remoteConn.Close()

	logger.Debug("已接收新的远端反向连接", "tunnel", t.cfg.Name, "client", remoteConn.RemoteAddr())

	metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Inc()
	defer metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Dec()

	localConn, err := net.Dial("tcp", t.cfg.LocalAddr)
	if err != nil {
		logger.Error("本地回源拨号失败", "tunnel", t.cfg.Name, "local_addr", t.cfg.LocalAddr, "err", err)
		return
	}
	defer localConn.Close()

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
	logger.Debug("连接已关闭", "tunnel", t.cfg.Name, "client", remoteConn.RemoteAddr())
}

// Stop 终止并清理反向隧道资源
func (t *RemoteTunnel) Stop() {
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

	go func() {
		t.wg.Wait()
		logger.Info("远端反向隧道已完全停止（旧连接已排空）。", "tunnel", t.cfg.Name)
	}()
}
