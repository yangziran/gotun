package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/pkg/logger"
)

var logLevel string

// rootCmd 代表在没有子命令时被调用的基础命令
var rootCmd = &cobra.Command{
	Use:   "gotun",
	Short: "Gotun - 一款轻量级的跨平台 SSH 隧道管理器",
	Long: `Gotun 是一个强大的、基于 Go 语言编写的 SSH 隧道管理工具。
它支持本地转发(Local)、动态代理(Dynamic)与反向内网穿透(Remote)，并内置智能心跳与退避重连机制。`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		logger.Init(logLevel)
	},
}

const usageTemplate = `使用方法:
  {{.UseLine}}

{{if .HasAvailableSubCommands}}可选命令:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}

{{if .HasAvailableLocalFlags}}可选参数:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}

{{if .HasAvailableInheritedFlags}}全局参数:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}

使用 "{{.CommandPath}} [command] --help" 获取有关某个命令的详细信息。
`

// Execute is the main entry point for the CLI
func Execute() {
	rootCmd.SetUsageTemplate(usageTemplate)
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// 覆盖内置的 help 命令
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: "查看任意命令的帮助信息",
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "设置日志输出级别 (debug, info, warn, error)")

	// 自定义 help 参数以覆盖默认的英文描述
	rootCmd.PersistentFlags().BoolP("help", "h", false, "显示帮助信息")
}
