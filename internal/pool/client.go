package pool

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/hostkey"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/pkg/logger"
	"github.com/yangziran/gotun/pkg/utils"
	"golang.org/x/crypto/ssh"
)

// Client 封装了单个靶机的 SSH 物理连接生命周期。
// 它内置了心跳保持、状态监控与断线自动退避重连机制，确保隧道持久不死。
type Client struct {
	srvCfg config.ServerConfig
	pool   *Pool

	mu        sync.RWMutex
	sshClient *ssh.Client
	readyChan chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newClient(ctx context.Context, srvCfg config.ServerConfig, pool *Pool) *Client {
	c := &Client{
		srvCfg:    srvCfg,
		pool:      pool,
		readyChan: make(chan struct{}),
	}
	c.ctx, c.cancel = context.WithCancel(ctx)

	c.wg.Add(1)
	go c.maintainConnection()
	return c
}

// GetSSHClient 阻塞等待直到 SSH 客户端连接就绪
func (c *Client) GetSSHClient(ctx context.Context) (*ssh.Client, error) {
	for {
		c.mu.RLock()
		client := c.sshClient
		ch := c.readyChan
		c.mu.RUnlock()

		if client != nil {
			return client, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ctx.Done():
			return nil, fmt.Errorf("client closed")
		case <-ch:
			// readyChan 被 close，连接状态改变，重试
		}
	}
}

func (c *Client) maintainConnection() {
	defer c.wg.Done()
	backoff := utils.NewBackoff(1*time.Second, 60*time.Second, 2.0)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		logger.Info("尝试连接 SSH 服务器", "server", c.srvCfg.Name)
		client, err := c.dial()
		if err != nil {
			metrics.SSHReconnects.WithLabelValues(c.srvCfg.Name).Inc()
			wait := backoff.Duration()
			logger.Error("SSH 服务器连接失败，准备退避重试", "server", c.srvCfg.Name, "err", err, "next_retry", wait)

			select {
			case <-time.After(wait):
			case <-c.ctx.Done():
				return
			}
			continue
		}

		logger.Info("SSH 服务器连接成功！", "server", c.srvCfg.Name)
		backoff.Reset()

		c.mu.Lock()
		c.sshClient = client
		close(c.readyChan) // 唤醒所有等待的隧道
		c.mu.Unlock()

		// 阻塞等待连接断开
		c.keepAlive(client)

		c.mu.Lock()
		if c.sshClient != nil {
			c.sshClient.Close()
		}
		c.sshClient = nil
		c.readyChan = make(chan struct{})
		c.mu.Unlock()
	}
}

// ctxConn 包装 net.Conn，在 ctx 被取消时关闭底层连接，
// 使阻塞中的 SSH 握手（ssh.NewClientConn）能够被中断返回错误。
// 握手成功后必须调用 release 解除监听，避免误关正常连接。
type ctxConn struct {
	net.Conn
	mu       sync.Mutex
	released bool
}

func newCtxConn(ctx context.Context, conn net.Conn) *ctxConn {
	cc := &ctxConn{Conn: conn}
	go func() {
		<-ctx.Done()
		cc.mu.Lock()
		released := cc.released
		cc.mu.Unlock()
		if !released {
			conn.Close()
		}
	}()
	return cc
}

func (cc *ctxConn) release() {
	cc.mu.Lock()
	cc.released = true
	cc.mu.Unlock()
}

func (c *Client) dial() (*ssh.Client, error) {
	verifier, err := hostkey.NewVerifier(c.srvCfg.Host, c.srvCfg.Port, c.srvCfg.HostKey)
	if err != nil {
		return nil, fmt.Errorf("初始化主机密钥校验失败: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            c.srvCfg.User,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: verifier.Callback, // TOFU：跳板与最终节点各自独立校验
		Timeout:         10 * time.Second,
	}

	if c.srvCfg.KeyPath != "" {
		keyPath := c.srvCfg.KeyPath
		if strings.HasPrefix(keyPath, "~/") {
			if homeDir, err := os.UserHomeDir(); err == nil {
				keyPath = filepath.Join(homeDir, keyPath[2:])
			}
		}
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("读取私钥失败: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		sshConfig.Auth = append(sshConfig.Auth, ssh.PublicKeys(signer))
	} else if c.srvCfg.Password != "" {
		sshConfig.Auth = append(sshConfig.Auth, ssh.Password(c.srvCfg.Password))
	}

	targetAddr := net.JoinHostPort(c.srvCfg.Host, strconv.Itoa(c.srvCfg.Port))

	// 链式代理穿透 (ProxyJump)
	if c.srvCfg.JumpHost != "" {
		jumpClient, err := c.pool.GetReadyClient(c.ctx, c.srvCfg.JumpHost)
		if err != nil {
			return nil, fmt.Errorf("无法获取堡垒机连接: %w", err)
		}

		conn, err := jumpClient.Dial("tcp", targetAddr)
		if err != nil {
			return nil, fmt.Errorf("通过堡垒机拨号失败: %w", err)
		}

		cc := newCtxConn(c.ctx, conn)
		ncc, chans, reqs, err := ssh.NewClientConn(cc, targetAddr, sshConfig)
		if err != nil {
			cc.release()
			conn.Close()
			return nil, err
		}
		cc.release()
		return ssh.NewClient(ncc, chans, reqs), nil
	}

	// 直连
	conn, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetReadBuffer(4 * 1024 * 1024)
		tcpConn.SetWriteBuffer(4 * 1024 * 1024)
	}

	cc := newCtxConn(c.ctx, conn)
	ncc, chans, reqs, err := ssh.NewClientConn(cc, targetAddr, sshConfig)
	if err != nil {
		cc.release()
		conn.Close()
		return nil, err
	}
	cc.release()
	return ssh.NewClient(ncc, chans, reqs), nil
}

func (c *Client) keepAlive(client *ssh.Client) {
	c.runKeepAlive(func() error {
		_, _, err := client.SendRequest("keepalive@gotun", true, nil)
		return err
	}, 15*time.Second, 10*time.Second)
}

// runKeepAlive 周期性执行心跳探测，回包阻塞超过 timeout 视为一次 missed；
// 连续 3 次 missed 判定连接假死并返回（由调用方重建连接）。
// 滞留的探测 goroutine 通过缓冲 channel 无损退出，不会阻塞泄漏。
func (c *Client) runKeepAlive(send func() error, interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	missed := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			done := make(chan error, 1)
			go func() {
				done <- send()
			}()

			select {
			case <-c.ctx.Done():
				return
			case err := <-done:
				if err != nil {
					missed++
					if missed >= 3 {
						logger.Warn("SSH 心跳丢失超限，断开连接", "server", c.srvCfg.Name)
						return
					}
				} else {
					missed = 0
				}
			case <-time.After(timeout):
				missed++
				if missed >= 3 {
					logger.Warn("SSH 心跳超时次数超限，断开连接", "server", c.srvCfg.Name)
					return
				}
			}
		}
	}
}

func (c *Client) Stop() {
	c.cancel()
	c.mu.Lock()
	if c.sshClient != nil {
		c.sshClient.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}
