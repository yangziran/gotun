//go:build !windows

package cli

import "syscall"

// pidAlive 判断指定 PID 的进程是否仍然存活
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if err == syscall.EPERM {
		// 进程存在，但当前用户无权限向其发信号
		return true
	}
	// ESRCH 等：进程不存在
	return false
}
