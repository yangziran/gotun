//go:build windows

package cli

import "golang.org/x/sys/windows"

// stillActive 是 Windows 的 STILL_ACTIVE 退出码 (259)，表示进程仍在运行
const stillActive = 259

// pidAlive 判断指定 PID 的进程是否仍然存活
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
