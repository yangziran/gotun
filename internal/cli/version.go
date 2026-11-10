package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version 为发布版本号。发布 CI 通过 -ldflags "-X github.com/yangziran/gotun/internal/cli.Version=<tag>"
// 注入对应 tag；本地开发构建无注入时显示 "dev"。
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印 Gotun 的当前版本号",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Gotun %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
