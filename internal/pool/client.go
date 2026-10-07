package pool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/metrics"
	"github.com/yangziran/gotun/pkg/logger"
	"github.com/yangziran/gotun/pkg/utils"
	"golang.org/x/crypto/ssh"
)

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

func (c *Client) dial() (*ssh.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User:            c.srvCfg.User,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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

	targetAddr := fmt.Sprintf("%s:%d", c.srvCfg.Host, c.srvCfg.Port)

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

		ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, sshConfig)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return ssh.NewClient(ncc, chans, reqs), nil
	}

	// 直连
	return ssh.Dial("tcp", targetAddr, sshConfig)
}

func (c *Client) keepAlive(client *ssh.Client) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	missed := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@gotun", true, nil)
			if err != nil {
				missed++
				if missed >= 3 {
					logger.Warn("SSH 心跳丢失超限，断开连接", "server", c.srvCfg.Name)
					return
				}
			} else {
				missed = 0
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
