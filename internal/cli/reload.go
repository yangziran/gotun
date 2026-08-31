package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/pkg/logger"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "向正在运行的 Gotun 进程发送热加载配置信号 (Hot Reload)",
	Run: func(cmd *cobra.Command, args []string) {
		pid, err := readPID()
		if err != nil {
			logger.Error("无法找到正在运行的 Gotun 进程", "err", err)
			os.Exit(1)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			logger.Error("进程不存在", "pid", pid, "err", err)
			os.Exit(1)
		}

		// 向进程发送热加载信号
		err = sendReloadSignal(process)
		if err != nil {
			logger.Error("发送热加载信号失败 (可能权限不足或进程已退出)", "pid", pid, "err", err)
			os.Exit(1)
		}

		fmt.Printf("✅ 成功向 gotun (PID: %d) 发送配置热加载信号！\n", pid)
		fmt.Println("请在服务运行的控制台或日志中查看配置重载结果。")
	},
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}
