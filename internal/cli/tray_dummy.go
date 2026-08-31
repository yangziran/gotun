//go:build !cgo && !windows

package cli

import (
	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/pkg/logger"
)

var trayCfgFile string

var trayCmd = &cobra.Command{
	Use:   "tray",
	Short: "启动系统状态栏 (当前编译环境不支持)",
	Run: func(cmd *cobra.Command, args []string) {
		logger.Error("状态栏 (Systray) 功能在当前二进制构建中未启用 (需要 CGO)。请使用官方 Release 提供的预编译版本，或在目标平台开启 CGO 重新编译。")
	},
}

func init() {
	trayCmd.Flags().StringVarP(&trayCfgFile, "config", "c", "", "配置文件的路径 (必需)")
	rootCmd.AddCommand(trayCmd)
}
