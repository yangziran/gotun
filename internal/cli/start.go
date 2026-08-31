package cli

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/manager"
	"github.com/yangziran/gotun/pkg/logger"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "根据配置文件启动所有的隧道",
	Run: func(cmd *cobra.Command, args []string) {
		if cfgFile == "" {
			logger.Error("必须指定配置文件 (-c)")
			os.Exit(1)
		}

		logger.Info("开始加载配置...", "file", cfgFile)
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			logger.Error("加载配置文件失败", "err", err)
			os.Exit(1)
		}

		logger.Info("成功加载配置", "servers", len(cfg.Servers), "tunnels", len(cfg.Tunnels))

		// 写入 PID 文件，供 gotun reload 命令使用
		if err := writePID(); err != nil {
			logger.Warn("写入 PID 文件失败，热加载命令可能无法使用", "err", err)
		}
		defer os.Remove(getPIDFilePath())

		m := manager.NewManager(cfg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := m.Start(ctx); err != nil {
			logger.Error("无法启动隧道管理器", "err", err)
			cancel()
			os.Exit(1)
		}

		// 监听操作系统信号以实现优雅关闭与热加载
		sigCh := make(chan os.Signal, 1)

		signalsToListen := append(getCloseSignals(), getReloadSignals()...)
		if len(signalsToListen) > 0 {
			signal.Notify(sigCh, signalsToListen...)
		}

		for sig := range sigCh {
			if isReloadSignal(sig) {
				logger.Info("捕获到热加载信号，准备重新加载配置...")
				newCfg, err := config.LoadConfig(cfgFile)
				if err != nil {
					logger.Error("热加载重读配置文件失败，将继续使用旧配置", "err", err)
				} else {
					m.Reload(newCfg)
				}
			} else {
				logger.Info("捕获到退出信号，准备优雅关闭服务...", "signal", sig)
				cancel()
				m.Stop()
				break
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().StringVarP(&cfgFile, "config", "c", "", "配置文件的路径 (必填)")
}
