package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "v1.0.0"

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
