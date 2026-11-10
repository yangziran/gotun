//go:build cgo || windows

package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/getlantern/systray"
	"github.com/spf13/cobra"
	"github.com/yangziran/gotun/internal/config"
	"github.com/yangziran/gotun/internal/manager"
	"github.com/yangziran/gotun/pkg/logger"
)

var trayCfgFile string

var trayCmd = &cobra.Command{
	Use:   "tray",
	Short: "启动带有系统状态栏 (System Tray) 的 Gotun",
	Run: func(cmd *cobra.Command, args []string) {
		if trayCfgFile == "" {
			logger.Error("必须指定配置文件 (-c)")
			return
		}
		engine := &trayEngine{}
		systray.Run(func() { onReady(engine) }, engine.stop)
	},
}

// trayEngine 封装托盘引擎的当前实例，通过互斥锁避免快速启停时的数据竞争
type trayEngine struct {
	mu      sync.Mutex
	manager *manager.Manager
	cancel  context.CancelFunc
}

func (e *trayEngine) start() {
	cfg, err := config.LoadConfig(trayCfgFile)
	if err != nil {
		logger.Error("加载配置失败", "err", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := manager.NewManager(cfg)

	e.mu.Lock()
	e.manager = m
	e.cancel = cancel
	e.mu.Unlock()

	go func() {
		if err := m.Start(ctx); err != nil {
			logger.Error("引擎启动失败", "err", err)
		}
	}()
}

func (e *trayEngine) stop() {
	e.mu.Lock()
	m := e.manager
	cancel := e.cancel
	e.manager = nil
	e.cancel = nil
	e.mu.Unlock()

	if m != nil {
		m.Stop()
	}
	if cancel != nil {
		cancel()
	}
}

// current 返回当前引擎实例的 Manager（可能为 nil）
func (e *trayEngine) current() *manager.Manager {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.manager
}

func onReady(engine *trayEngine) {
	systray.SetIcon(iconData)
	systray.SetTooltip("Gotun - 极速代理引擎")

	statusItem := systray.AddMenuItem("状态: 启动中...", "当前引擎状态")
	statusItem.Disable()
	systray.AddSeparator()

	toggleItem := systray.AddMenuItem("停止代理", "启动或停止代理核心")
	reloadItem := systray.AddMenuItem("重新加载配置", "热重载配置文件")
	editItem := systray.AddMenuItem("打开配置目录", "编辑 config.yaml")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("退出 Gotun", "完全退出")

	// 初始启动引擎
	engine.start()
	statusItem.SetTitle("状态: 运行中")

	go func() {
		isRunning := true
		for {
			select {
			case <-toggleItem.ClickedCh:
				if isRunning {
					engine.stop()
					toggleItem.SetTitle("启动代理")
					statusItem.SetTitle("状态: 已停止")
					isRunning = false
				} else {
					engine.start()
					toggleItem.SetTitle("停止代理")
					statusItem.SetTitle("状态: 运行中")
					isRunning = true
				}
			case <-reloadItem.ClickedCh:
				if isRunning {
					if m := engine.current(); m != nil {
						cfg, err := config.LoadConfig(trayCfgFile)
						if err != nil {
							logger.Error("配置重新加载失败", "err", err)
						} else {
							m.Reload(cfg)
							logger.Info("状态栏触发配置热重载成功")
						}
					}
				}
			case <-editItem.ClickedCh:
				absPath, _ := filepath.Abs(trayCfgFile)
				dir := filepath.Dir(absPath)
				_ = exec.Command("open", dir).Start() // macOS open dir
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func init() {
	rootCmd.AddCommand(trayCmd)
	trayCmd.Flags().StringVarP(&trayCfgFile, "config", "c", "", "配置文件的路径 (必填)")
}
