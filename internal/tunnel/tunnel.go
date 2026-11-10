package tunnel

import (
	"context"
	"net"
	"sync"
)

// Tunnel 定义隧道的基础接口
type Tunnel interface {
	// Start 启动隧道转发
	Start(ctx context.Context) error

	// Stop 停止隧道并清理资源
	Stop()

	// GetName 返回隧道名称
	GetName() string
}

// connTracker 跟踪隧道的活动连接，并协调 Stop 后的异步排空与下一次 Start：
// Stop 会关闭全部活动连接并创建排空通道，wg.Wait 完成后由 drained 关闭该通道；
// 再次 Start 前必须等待该通道，避免 wg.Add 与上一代 wg.Wait 并发产生数据竞争。
type connTracker struct {
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	stopped bool
	drainCh chan struct{} // 当前代的排空通道，由 stop 创建、drained 关闭并置 nil
}

// start 开启新一代连接跟踪，返回需要等待的上代排空通道（可能为 nil）
func (ct *connTracker) start() <-chan struct{} {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.stopped = false
	ct.conns = make(map[net.Conn]struct{})
	return ct.drainCh
}

// add 登记活动连接；若隧道已停止则返回 false，调用方应立即关闭该连接
func (ct *connTracker) add(c net.Conn) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if ct.stopped {
		return false
	}
	ct.conns[c] = struct{}{}
	return true
}

// remove 移除已结束的活动连接
func (ct *connTracker) remove(c net.Conn) {
	ct.mu.Lock()
	delete(ct.conns, c)
	ct.mu.Unlock()
}

// stop 标记停止并关闭全部活动连接，创建本代排空通道
func (ct *connTracker) stop() {
	ct.mu.Lock()
	ct.stopped = true
	conns := ct.conns
	ct.conns = nil
	ct.drainCh = make(chan struct{})
	ct.mu.Unlock()

	for c := range conns {
		c.Close()
	}
}

// drained 关闭本代排空通道，允许下一代 Start 继续
func (ct *connTracker) drained() {
	ct.mu.Lock()
	if ct.drainCh != nil {
		close(ct.drainCh)
		ct.drainCh = nil
	}
	ct.mu.Unlock()
}
