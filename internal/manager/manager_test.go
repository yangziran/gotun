package manager

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/yangziran/gotun/internal/config"
)

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

func localTunnelCfg(name, addr string) config.TunnelConfig {
	return config.TunnelConfig{
		Name:       name,
		Type:       "local",
		LocalAddr:  addr,
		RemoteAddr: "127.0.0.1:9", // 不可达目标即可，测试不依赖真实 SSH
	}
}

// TestManagerStartStopReleasesListener 验证 Manager 停止后隧道监听器完全释放
func TestManagerStartStopReleasesListener(t *testing.T) {
	addr := freeAddr(t)
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-l1", addr)},
	}

	m := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Manager Start 失败: %v", err)
	}

	// 确认监听器存活
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("启动后无法拨号本地监听: %v", err)
	}
	_ = conn.Close()

	m.Stop()
	assertPortFree(t, addr)
}

// TestManagerStopDuringStart 模拟 Stop/Reload 与 Start 并发：
// 旧实现中 Start 在 goroutine 里绑定监听，Stop 可能先行返回导致监听器泄漏
func TestManagerStopDuringStart(t *testing.T) {
	addr := freeAddr(t)
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-race", addr)},
	}

	m := NewManager(cfg)
	go func() {
		// Start 内部同步绑定；无论与 Stop 如何交错，Stop 后都不应泄漏
		if err := m.Start(context.Background()); err != nil {
			t.Errorf("Manager Start 失败: %v", err)
		}
	}()

	time.Sleep(10 * time.Millisecond) // 让 Start 处于绑定过程中
	m.Stop()

	assertPortFree(t, addr)
}

// TestManagerReloadRemovesTunnel 验证热加载删除隧道后监听器被释放
func TestManagerReloadRemovesTunnel(t *testing.T) {
	addr := freeAddr(t)
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-old", addr)},
	}

	m := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Manager Start 失败: %v", err)
	}

	// 热加载为空配置，应停止并移除旧隧道
	m.Reload(&config.Config{})

	assertPortFree(t, addr)
	m.Stop()
}

// TestManagerReloadRestartChangedTunnel 验证配置变更时旧隧道被停止且端口可复用
func TestManagerReloadRestartChangedTunnel(t *testing.T) {
	addr := freeAddr(t)
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-cfg", addr)},
	}

	m := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Manager Start 失败: %v", err)
	}

	// 变更 LocalAddr 触发重启
	newAddr := freeAddr(t)
	m.Reload(&config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-cfg", newAddr)},
	})

	assertPortFree(t, addr)

	// 新隧道应已在新地址监听
	conn, err := net.DialTimeout("tcp", newAddr, time.Second)
	if err != nil {
		t.Fatalf("热加载后新地址无法监听: %v", err)
	}
	_ = conn.Close()

	m.Stop()
	assertPortFree(t, newAddr)
}

// TestManagerStartFailureDoesNotRetainTunnel 验证启动失败的隧道不会残留在管理表中
func TestManagerStartFailureDoesNotRetainTunnel(t *testing.T) {
	// 占用地址后再让隧道监听同一地址：IPv4 绑定失败、IPv6 绑定也失败的唯一方式是
	// 使用非法地址，这里直接使用非法地址触发启动失败
	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{localTunnelCfg("m-bad", "invalid-addr")},
	}

	m := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx); err != nil {
		t.Fatalf("个别隧道启动失败不应导致 Manager Start 返回错误: %v", err)
	}

	m.mu.Lock()
	_, retained := m.tunnels["m-bad"]
	m.mu.Unlock()
	if retained {
		t.Fatal("启动失败的隧道不应残留在管理表中（否则 Reload 无法重试）")
	}
	m.Stop()
}
