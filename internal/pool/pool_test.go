package pool

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/yangziran/gotun/internal/config"
)

// testPool 构造一个最小可用的测试用 Pool 与 Client。
// GOTUN_KNOWN_HOSTS 指向临时文件，避免 hostkey.NewVerifier 因无法确定路径而失败。
func testEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOTUN_KNOWN_HOSTS", filepath.Join(t.TempDir(), "known_hosts"))
}

func newTestClient(t *testing.T, cfg config.ServerConfig) *Client {
	t.Helper()
	c := &Client{
		srvCfg:    cfg,
		pool:      NewPool(context.Background()),
		readyChan: make(chan struct{}),
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t.Cleanup(c.cancel)
	return c
}

// 缺陷 1 回归测试：带 ProxyJump 的节点在 Stop 时不死锁。
// via 节点的 dial 会调用 pool.GetReadyClient("jump") 获取 p.mu 读锁；
// 旧实现中 Pool.Stop 持写锁等待 Client 退出、Client 又阻塞在读锁上，构成死锁。
func TestPoolStopWithProxyJumpNoDeadlock(t *testing.T) {
	testEnv(t)
	p := NewPool(context.Background())
	defer p.Stop()

	p.UpdateServers([]config.ServerConfig{
		{Name: "jump", Host: "127.0.0.1", Port: 1, User: "u", Password: "p"},
		{Name: "via", Host: "127.0.0.1", Port: 2, User: "u", Password: "p", JumpHost: "jump"},
	})

	// 给 via 的 Client 留出时间进入 dial -> GetReadyClient（阻塞在 p.mu 读锁上）
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Pool.Stop 死锁：等待超过 5 秒仍未返回")
	}
}

// 缺陷 1 回归测试：UpdateServers 更新 ProxyJump 节点配置（需停止旧组）时不死锁。
func TestUpdateServersWithProxyJumpNoDeadlock(t *testing.T) {
	testEnv(t)
	p := NewPool(context.Background())
	defer p.Stop()

	jump := config.ServerConfig{Name: "jump", Host: "127.0.0.1", Port: 1, User: "u", Password: "p"}
	via := config.ServerConfig{Name: "via", Host: "127.0.0.1", Port: 2, User: "u", Password: "p", JumpHost: "jump"}
	p.UpdateServers([]config.ServerConfig{jump, via})

	// 给 via 的 Client 留出时间进入 dial -> GetReadyClient（阻塞在 p.mu 读锁上）
	time.Sleep(300 * time.Millisecond)

	// via 端口变化，触发"配置变化需停止旧组"路径；同时移除 jump，触发"被移除需停止"路径
	updated := []config.ServerConfig{
		{Name: "jump2", Host: "127.0.0.1", Port: 1, User: "u", Password: "p"},
		{Name: "via", Host: "127.0.0.1", Port: 3, User: "u", Password: "p", JumpHost: "jump2"},
	}

	done := make(chan struct{})
	go func() {
		p.UpdateServers(updated)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateServers 死锁：等待超过 5 秒仍未返回")
	}
}

// 缺陷 2 回归测试：cancel 后阻塞中的 SSH 握手应被打断，dial 返回错误而非永久阻塞。
// 使用一个接受连接但从不发送 SSH banner 的黑洞 listener，使 NewClientConn 永久阻塞在读上。
func TestDialCancelUnblocksHandshake(t *testing.T) {
	testEnv(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close() // 持有连接但不写任何数据
		}
	}()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}

	c := newTestClient(t, config.ServerConfig{
		Name:     "t",
		Host:     "127.0.0.1",
		Port:     port,
		User:     "u",
		Password: "p",
	})

	done := make(chan error, 1)
	go func() {
		_, err := c.dial()
		done <- err
	}()

	// 让 dial 进入阻塞中的 SSH 握手
	time.Sleep(300 * time.Millisecond)
	c.cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("期望 dial 在 cancel 后返回错误，实际返回成功")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dial 在 cancel 后仍永久阻塞（SSH 握手不可取消）")
	}
}

// 缺陷 2 机制测试：ctxConn 在 ctx 取消时关闭底层连接；
// release 之后则只响应 release，不再因 cancel 关闭正常连接。
func TestCtxConn(t *testing.T) {
	// 未 release：cancel 应关闭底层连接
	ctx, cancel := context.WithCancel(context.Background())
	c1, c2 := net.Pipe()
	newCtxConn(ctx, c1)
	cancel()
	if err := c2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("设置读超时失败: %v", err)
	}
	if _, err := c2.Read(make([]byte, 1)); err == nil {
		t.Fatal("期望 cancel 后底层连接被关闭，Read 返回错误")
	}

	// 已 release：cancel 不应再关闭底层连接
	ctx2, cancel2 := context.WithCancel(context.Background())
	d1, d2 := net.Pipe()
	defer d1.Close()
	defer d2.Close()
	cc2 := newCtxConn(ctx2, d1)
	cc2.release()
	cancel2()

	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		d1.Read(buf)
		close(readDone)
	}()
	if err := d2.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("设置写超时失败: %v", err)
	}
	if _, err := d2.Write([]byte("x")); err != nil {
		t.Fatalf("release 后 cancel 不应关闭连接: %v", err)
	}
	<-readDone
}
