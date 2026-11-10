package tunnel

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/pool"
)

// freeAddr 获取一个当前空闲的本地回环地址
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// assertPortFree 在超时时间内反复断言指定地址可以被重新监听（即旧监听器已释放）
func assertPortFree(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("端口 %s 在 Stop 后仍未释放: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertConnClosed 断言连接已被对端关闭
func assertConnClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, err := c.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("预期连接已被关闭，但读取仍成功")
	}
}

// waitGroupDone 在超时时间内等待 WaitGroup 完成，用于检测 goroutine 泄漏
func waitGroupDone(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("隧道 goroutine 在 Stop 后未退出，疑似泄漏")
	}
}

func localTestCfg(name, addr string) config.TunnelConfig {
	return config.TunnelConfig{
		Name:       name,
		Type:       "local",
		LocalAddr:  addr,
		RemoteAddr: "127.0.0.1:9", // 不可达目标即可，测试不依赖真实 SSH
	}
}

func TestLocalTunnelStopClosesListener(t *testing.T) {
	addr := freeAddr(t)
	ctx := context.Background()
	tun := NewLocalTunnel(localTestCfg("t-stop-listener", addr), pool.NewPool(ctx))

	if err := tun.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 确认监听器存活
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("启动后无法拨号本地监听: %v", err)
	}
	_ = conn.Close()

	tun.Stop()
	assertPortFree(t, addr)
	waitGroupDone(t, &tun.wg)
}

func TestLocalTunnelStartStopRaceNoLeak(t *testing.T) {
	addr := freeAddr(t)
	ctx := context.Background()
	tun := NewLocalTunnel(localTestCfg("t-race", addr), pool.NewPool(ctx))

	// 快速交替 Start/Stop，不应泄漏监听器
	for i := 0; i < 20; i++ {
		if err := tun.Start(ctx); err != nil {
			t.Fatalf("第 %d 次 Start 失败: %v", i, err)
		}
		tun.Stop()
	}
	assertPortFree(t, addr)
}

// TestLocalTunnelStopDuringStart 模拟 Stop/父 ctx 取消与 Start 并发的场景，
// 无论 Start 先完成绑定还是中途被取消，都不应泄漏监听器
func TestLocalTunnelStopDuringStart(t *testing.T) {
	addr := freeAddr(t)
	parentCtx, parentCancel := context.WithCancel(context.Background())
	tun := NewLocalTunnel(localTestCfg("t-stop-during-start", addr), pool.NewPool(parentCtx))

	done := make(chan error, 1)
	go func() {
		done <- tun.Start(parentCtx)
	}()

	// 模拟 Manager 停止：先取消父上下文，再调用 Stop
	parentCancel()
	tun.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start 在父上下文取消后未及时返回")
	}
	assertPortFree(t, addr)
	waitGroupDone(t, &tun.wg)
}

func TestLocalTunnelStartAbortsWhenCtxCancelled(t *testing.T) {
	addr := freeAddr(t)
	parentCtx, parentCancel := context.WithCancel(context.Background())
	parentCancel() // 预先取消

	tun := NewLocalTunnel(localTestCfg("t-ctx-abort", addr), pool.NewPool(parentCtx))
	if err := tun.Start(parentCtx); err == nil {
		t.Fatal("父上下文已取消时 Start 应返回错误")
	}
	assertPortFree(t, addr)

	// 失败的 Start 不应留下运行状态，Stop 不应 panic
	tun.Stop()
}

func TestLocalTunnelStartFailsOnInvalidAddr(t *testing.T) {
	ctx := context.Background()
	tun := NewLocalTunnel(localTestCfg("t-invalid", "invalid-addr"), pool.NewPool(ctx))
	if err := tun.Start(ctx); err == nil {
		t.Fatal("非法监听地址应导致 Start 失败")
	}
	// 失败后再次 Start 应报“已运行”以外的错误或成功重置状态，此处验证 Stop 安全
	tun.Stop()
}

func TestLocalTunnelStopClosesActiveConns(t *testing.T) {
	addr := freeAddr(t)
	ctx := context.Background()
	tun := NewLocalTunnel(localTestCfg("t-close-conns", addr), pool.NewPool(ctx))

	if err := tun.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 白盒方式登记一条活动连接（不依赖真实 SSH 拨号）
	c1, c2 := net.Pipe()
	defer c2.Close()
	if !tun.tracker.add(c1) {
		t.Fatal("运行中的隧道应接受活动连接登记")
	}

	tun.Stop()
	assertConnClosed(t, c1)
	_ = c1.Close()
	waitGroupDone(t, &tun.wg)
}

func TestDynamicTunnelStopClosesListenerAndConns(t *testing.T) {
	addr := freeAddr(t)
	ctx := context.Background()
	cfg := config.TunnelConfig{Name: "d-stop", Type: "dynamic", LocalAddr: addr}
	tun := NewDynamicTunnel(cfg, pool.NewPool(ctx), nil)

	if err := tun.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("启动后无法拨号 SOCKS5 监听: %v", err)
	}
	_ = conn.Close()

	// 登记一条活动连接，验证 Stop 会统一断开
	c1, c2 := net.Pipe()
	defer c2.Close()
	if !tun.tracker.add(c1) {
		t.Fatal("运行中的动态隧道应接受活动连接登记")
	}

	tun.Stop()
	assertConnClosed(t, c1)
	_ = c1.Close()
	assertPortFree(t, addr)
	waitGroupDone(t, &tun.wg)
}

func TestDynamicTunnelStartStopRaceNoLeak(t *testing.T) {
	addr := freeAddr(t)
	ctx := context.Background()
	cfg := config.TunnelConfig{Name: "d-race", Type: "dynamic", LocalAddr: addr}
	tun := NewDynamicTunnel(cfg, pool.NewPool(ctx), nil)

	for i := 0; i < 20; i++ {
		if err := tun.Start(ctx); err != nil {
			t.Fatalf("第 %d 次 Start 失败: %v", i, err)
		}
		tun.Stop()
	}
	assertPortFree(t, addr)
}

func TestRemoteTunnelStopClosesActiveConns(t *testing.T) {
	ctx := context.Background()
	cfg := config.TunnelConfig{
		Name:       "r-stop",
		Type:       "remote",
		LocalAddr:  "127.0.0.1:9",
		RemoteAddr: "127.0.0.1:9",
	}
	tun := NewRemoteTunnel(cfg, pool.NewPool(ctx))

	if err := tun.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 白盒登记活动连接，模拟 remoteAcceptLoop 中正在转发的会话
	c1, c2 := net.Pipe()
	defer c2.Close()
	if !tun.tracker.add(c1) {
		t.Fatal("运行中的远端隧道应接受活动连接登记")
	}

	tun.Stop()
	assertConnClosed(t, c1)
	_ = c1.Close()
	waitGroupDone(t, &tun.wg)
}

func TestRemoteTunnelStartStopRace(t *testing.T) {
	ctx := context.Background()
	cfg := config.TunnelConfig{
		Name:       "r-race",
		Type:       "remote",
		LocalAddr:  "127.0.0.1:9",
		RemoteAddr: "127.0.0.1:9",
	}
	tun := NewRemoteTunnel(cfg, pool.NewPool(ctx))

	for i := 0; i < 20; i++ {
		if err := tun.Start(ctx); err != nil {
			t.Fatalf("第 %d 次 Start 失败: %v", i, err)
		}
		tun.Stop()
	}
	waitGroupDone(t, &tun.wg)
}
