package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func getPIDFilePath() string {
	return filepath.Join(os.TempDir(), "gotun.pid")
}

func writePID() error {
	pidFile := getPIDFilePath()

	// Stale 检测：PID 文件已存在且对应进程仍存活时，拒绝覆盖，避免误杀其他进程
	if data, err := os.ReadFile(pidFile); err == nil {
		if oldPID, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pidAlive(oldPID) {
			return fmt.Errorf("检测到 gotun 已在运行 (PID: %d)，如确认是残留进程请手动删除 %s", oldPID, pidFile)
		}
	}

	f, err := os.OpenFile(pidFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return err
	}
	// 兜底：文件已存在时 OpenFile 不会修改其权限，强制收敛为 0640
	_ = f.Chmod(0640)
	return nil
}

func readPID() (int, error) {
	pidFile := getPIDFilePath()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("读取 PID 文件失败 (可能未启动 gotun): %w", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("解析 PID 失败: %w", err)
	}
	return pid, nil
}
