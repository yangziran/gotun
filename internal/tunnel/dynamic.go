package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/internal/pool"
	"github.com/yangziran/gotun/internal/router"
	"github.com/yangziran/gotun/pkg/logger"
	"github.com/yangziran/gotun/pkg/utils"
)

// DynamicTunnel 实现了基于 SOCKS5 协议的动态端口转发隧道 (类似 ssh -D)
type DynamicTunnel struct {
	cfg    config.TunnelConfig
	pool   *pool.Pool
	router *router.Router

	listeners []net.Listener // 本地 SOCKS5 代理监听器列表
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	mu        sync.Mutex

	robinIndex uint64
}

// NewDynamicTunnel 创建一个新的 SOCKS5 动态代理隧道实例
func NewDynamicTunnel(cfg config.TunnelConfig, p *pool.Pool, r *router.Router) *DynamicTunnel {
	return &DynamicTunnel{
		cfg:    cfg,
		pool:   p,
		router: r,
	}
}

// GetName 返回隧道名称
func (t *DynamicTunnel) GetName() string {
	return t.cfg.Name
}

// Start 启动隧道：建立本地 SOCKS5 监听，并在后台派生请求接收协程
func (t *DynamicTunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("tunnel %s is already running", t.cfg.Name)
	}
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.running = true
	t.mu.Unlock()

	logger.Info("正在启动动态 SOCKS5 代理...", "tunnel", t.cfg.Name)

	addrs := utils.GetDualStackAddrs(t.cfg.LocalAddr)
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			logger.Warn("双栈监听失败 (可能不支持IPv6，将忽略)", "tunnel", t.cfg.Name, "addr", addr, "err", err)
			continue
		}
		t.listeners = append(t.listeners, ln)
		logger.Info("本地 SOCKS5 代理正在监听", "tunnel", t.cfg.Name, "local", addr, "servers", t.cfg.ServerNames)
	}

	if len(t.listeners) == 0 {
		return fmt.Errorf("listen on local addresses failed for tunnel %s", t.cfg.Name)
	}

	for _, ln := range t.listeners {
		t.wg.Add(1)
		go t.acceptLoop(ln)
	}

	return nil
}

// acceptLoop 接收 SOCKS5 客户端的连接请求
func (t *DynamicTunnel) acceptLoop(ln net.Listener) {
	defer t.wg.Done()
	for {
		localConn, err := ln.Accept()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			default:
				logger.Error("接收 SOCKS5 连接失败", "tunnel", t.cfg.Name, "err", err)
				return
			}
		}

		if tcpConn, ok := localConn.(*net.TCPConn); ok {
			tcpConn.SetNoDelay(true)
			tcpConn.SetReadBuffer(1 * 1024 * 1024)
			tcpConn.SetWriteBuffer(1 * 1024 * 1024)
		}

		t.wg.Add(1)
		go t.handleConnection(localConn)
	}
}

// handleConnection 负责处理单次 SOCKS5 会话（握手、寻址、数据转发）并融入负载均衡与指标采集
func (t *DynamicTunnel) handleConnection(localConn net.Conn) {
	defer t.wg.Done()
	defer localConn.Close()

	if err := t.socks5Handshake(localConn); err != nil {
		logger.Error("SOCKS5 握手失败", "tunnel", t.cfg.Name, "err", err)
		return
	}

	targetAddr, err := t.socks5Request(localConn)
	if err != nil {
		logger.Debug("SOCKS5 请求解析失败", "tunnel", t.cfg.Name, "client", localConn.RemoteAddr(), "err", err)
		return
	}

	host, _, err := net.SplitHostPort(targetAddr)
	if err != nil {
		logger.Debug("目标地址解析失败", "target", targetAddr, "err", err)
		return
	}

	metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Inc()
	defer metrics.ActiveConnections.WithLabelValues(t.cfg.Name, t.cfg.Type).Dec()

	var remoteConn net.Conn

	if t.router != nil && !t.router.ShouldProxy(host) {
		logger.Debug("SOCKS5 命中直连规则", "tunnel", t.cfg.Name, "target", targetAddr)
		remoteConn, err = net.Dial("tcp", targetAddr)
		if err != nil {
			logger.Error("直连目标失败", "tunnel", t.cfg.Name, "target", targetAddr, "err", err)
			localConn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			return
		}
	} else {
		if len(t.cfg.ServerNames) == 0 {
			logger.Error("拒绝连接：未配置 ServerNames 且未命中直连", "tunnel", t.cfg.Name)
			localConn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			return
		}

		// 轮询负载均衡
		idx := atomic.AddUint64(&t.robinIndex, 1)
		targetServer := t.cfg.ServerNames[idx%uint64(len(t.cfg.ServerNames))]

		logger.Debug("SOCKS5 寻址完成，准备派发代理", "tunnel", t.cfg.Name, "target", targetAddr, "dispatch_to", targetServer)

		client, err := t.pool.GetReadyClient(t.ctx, targetServer)
		if err != nil {
			logger.Error("拒绝连接：SSH 连接池不可用", "tunnel", t.cfg.Name, "client", localConn.RemoteAddr(), "err", err)
			localConn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			return
		}

		remoteConn, err = client.Dial("tcp", targetAddr)
		if err != nil {
			logger.Error("动态路由远端拨号失败", "tunnel", t.cfg.Name, "target", targetAddr, "err", err)
			localConn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			return
		}
	}
	defer remoteConn.Close()

	// 发送握手成功回复 (Success Reply)
	reply := []byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := localConn.Write(reply); err != nil {
		return
	}

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
}

// socks5Handshake 处理 SOCKS5 协议的认证握手阶段
func (t *DynamicTunnel) socks5Handshake(conn net.Conn) error {
	buf := make([]byte, 256)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return err
	}
	if buf[0] != 0x05 {
		return errors.New("unsupported SOCKS version")
	}
	nmethods := int(buf[1])

	if _, err := io.ReadFull(conn, buf[:nmethods]); err != nil {
		return err
	}

	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

// socks5Request 解析 SOCKS5 客户端的连接请求并提取目标地址
func (t *DynamicTunnel) socks5Request(conn net.Conn) (string, error) {
	buf := make([]byte, 256)
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", errors.New("unsupported SOCKS version")
	}
	if buf[1] != 0x01 {
		return "", errors.New("unsupported SOCKS command")
	}

	atyp := buf[3]
	var host string

	switch atyp {
	case 0x01: // IPv4 地址
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return "", err
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // 域名
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", err
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:domainLen]); err != nil {
			return "", err
		}
		host = string(buf[:domainLen])
	case 0x04: // IPv6 地址
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return "", err
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", errors.New("unsupported ATYP")
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(buf[:2])

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// Stop 终止并清理动态隧道资源
func (t *DynamicTunnel) Stop() {
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

	logger.Info("正在停止动态隧道...", "tunnel", t.cfg.Name)
	for _, ln := range t.listeners {
		ln.Close()
	}

	go func() {
		t.wg.Wait()
		logger.Info("动态隧道已完全停止（旧连接已排空）。", "tunnel", t.cfg.Name)
	}()
}
