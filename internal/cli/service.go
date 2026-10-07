package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/manager"
	"github.com/yangziran/gotun/pkg/logger"
)

var (
	svcConfigPath string
)

type program struct {
	m      *manager.Manager
	cancel context.CancelFunc
}

func (p *program) Start(s service.Service) error {
	// Start 方法不能阻塞，所以需要开一个 goroutine 跑主逻辑
	go p.run()
	return nil
}

func (p *program) run() {
	logger.Info("后台守护进程开始运行，加载配置...", "file", svcConfigPath)
	cfg, err := config.LoadConfig(svcConfigPath)
	if err != nil {
		logger.Error("后台守护进程加载配置文件失败", "err", err)
		return
	}

	// 写入 PID 文件，供 gotun reload 命令使用
	if err := writePID(); err != nil {
		logger.Warn("写入 PID 文件失败，热加载命令可能无法使用", "err", err)
	}
	defer os.Remove(getPIDFilePath())

	p.m = manager.NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	if err := p.m.Start(ctx); err != nil {
		logger.Error("后台无法启动隧道管理器", "err", err)
		cancel()
		return
	}

	sigCh := make(chan os.Signal, 1)
	reloadSignals := getReloadSignals()
	if len(reloadSignals) > 0 {
		signal.Notify(sigCh, reloadSignals...)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
			logger.Info("后台守护进程捕获到热加载信号，准备重新加载配置...")
			newCfg, err := config.LoadConfig(svcConfigPath)
			if err != nil {
				logger.Error("热加载重读配置文件失败，将继续使用旧配置", "err", err)
			} else {
				p.m.Reload(newCfg)
			}
		}
	}
}

func (p *program) Stop(s service.Service) error {
	logger.Info("系统发出终止信号，正在停止后台服务...")
	if p.cancel != nil {
		p.cancel()
	}
	if p.m != nil {
		p.m.Stop()
	}
	return nil
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "管理系统的后台守护进程 (支持开机自启、静默运行)",
}

// serviceRunCmd 是供系统后台系统 (Systemd/Launchd) 拉起的真实入口，对用户隐藏
var serviceRunCmd = &cobra.Command{
	Use:    "run",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		if svcConfigPath == "" {
			logger.Error("后台启动必须指定绝对路径的配置文件 (-c)")
			os.Exit(1)
		}

		svcConfig := &service.Config{
			Name:        "gotun",
			DisplayName: "Gotun 守护进程",
			Description: "一款强大的跨平台 SSH 隧道管理器",
		}

		prg := &program{}
		s, err := service.New(prg, svcConfig)
		if err != nil {
			logger.Error("创建系统服务对象失败", "err", err)
			os.Exit(1)
		}

		if err := s.Run(); err != nil {
			logger.Error("系统服务运行异常", "err", err)
			os.Exit(1)
		}
	},
}

func controlService(action string) error {
	if action == "install" && svcConfigPath == "" {
		return fmt.Errorf("安装守护进程时必须指定配置文件 (-c)")
	}

	var absConfigPath string
	if svcConfigPath != "" {
		var err error
		absConfigPath, err = filepath.Abs(svcConfigPath)
		if err != nil {
			return fmt.Errorf("解析配置文件的绝对路径失败: %w", err)
		}
	}

	svcConfig := &service.Config{
		Name:        "gotun",
		DisplayName: "Gotun 守护进程",
		Description: "一款强大的跨平台 SSH 隧道管理器",
		Arguments:   []string{"service", "run", "-c", absConfigPath},
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		return fmt.Errorf("创建系统服务对象失败: %w", err)
	}

	switch action {
	case "install":
		err = s.Install()
	case "uninstall":
		err = s.Uninstall()
	case "start":
		err = s.Start()
	case "stop":
		err = s.Stop()
	case "restart":
		err = s.Restart()
	case "status":
		status, err := s.Status()
		if err != nil {
			return fmt.Errorf("获取服务状态失败: %w", err)
		}
		statusStr := "未知 (Unknown)"
		if status == service.StatusRunning {
			statusStr = "正在运行 (Running)"
		} else if status == service.StatusStopped {
			statusStr = "已停止 (Stopped)"
		}
		fmt.Printf("Gotun 守护进程状态: %s\n", statusStr)
		return nil
	default:
		return fmt.Errorf("未知的系统操作: %s", action)
	}

	if err != nil {
		return fmt.Errorf("执行 %s 操作失败: %w (请确保您使用了管理员/sudo权限)", action, err)
	}
	fmt.Printf("成功执行 %s 操作！\n", action)
	return nil
}

func init() {
	rootCmd.AddCommand(serviceCmd)

	serviceCmd.AddCommand(serviceRunCmd)
	serviceRunCmd.Flags().StringVarP(&svcConfigPath, "config", "c", "", "配置文件的绝对路径")

	actions := map[string]string{
		"install":   "安装服务并设置开机自启",
		"uninstall": "卸载系统后台服务",
		"start":     "启动守护进程",
		"stop":      "停止守护进程",
		"restart":   "重启守护进程",
		"status":    "查看守护进程运行状态",
	}

	// 为保持 help 的输出顺序固定，可以手动按顺序遍历
	orderedActions := []string{"install", "uninstall", "start", "stop", "restart", "status"}

	for _, action := range orderedActions {
		act := action // 捕获循环变量以在闭包中使用
		desc := actions[act]
		cmd := &cobra.Command{
			Use:   act,
			Short: desc,
			Run: func(cmd *cobra.Command, args []string) {
				if err := controlService(act); err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
			},
		}
		if act == "install" {
			cmd.Flags().StringVarP(&svcConfigPath, "config", "c", "", "配置文件的路径 (必填)")
		}
		serviceCmd.AddCommand(cmd)
	}
}
