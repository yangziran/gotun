package tunnel

import "context"

// Tunnel 定义隧道的基础接口
type Tunnel interface {
	// Start 启动隧道转发
	Start(ctx context.Context) error

	// Stop 停止隧道并清理资源
	Stop()

	// GetName 返回隧道名称
	GetName() string
}
