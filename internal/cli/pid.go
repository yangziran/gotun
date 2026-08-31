package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func getPIDFilePath() string {
	return filepath.Join(os.TempDir(), "gotun.pid")
}

func writePID() error {
	pid := os.Getpid()
	pidFile := getPIDFilePath()
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
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
