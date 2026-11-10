package pool

import (
	"context"
	"testing"
	"time"

	"github.com/yangziran/gotun/internal/config"
)

// 模拟 SendRequest 永不返回的半开连接：心跳应按时超时并累计 missed，
// 连续 3 次 missed 后 runKeepAlive 返回（判定死亡，由调用方重建）。
func TestKeepAliveTimeoutCountsMissed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Client{
		srvCfg: config.ServerConfig{Name: "test-server"},
		ctx:    ctx,
	}

	// 永不返回的探测函数，仅随 ctx 取消而退出
	blocked := make(chan struct{})
	defer close(blocked)
	send := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-blocked:
		}
		return nil
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		c.runKeepAlive(send, 10*time.Millisecond, 30*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("keepAlive 未能在 3 次超时后判定死亡并返回")
	}

	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("keepAlive 返回过早 (%v)，疑似未等满 3 次超时", elapsed)
	}
}

// 心跳正常回包时不计入 missed，仅在 ctx 取消时退出
func TestKeepAliveSuccessRunsUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Client{
		srvCfg: config.ServerConfig{Name: "test-server"},
		ctx:    ctx,
	}

	done := make(chan struct{})
	go func() {
		c.runKeepAlive(func() error { return nil }, 10*time.Millisecond, time.Second)
		close(done)
	}()

	// 让多轮成功心跳跑完，确认不会因误判 missed 而提前返回
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepAlive 未随 ctx 取消退出")
	}
}
