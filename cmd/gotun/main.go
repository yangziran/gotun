// package main 是 Gotun 程序的启动入口
package main

import (
	"github.com/yangziran/gotun/internal/cli"
)

// main 程序的入口函数
// 它将控制权交由内部的 CLI 模块 (Cobra) 进行命令行参数解析和守护进程调度
func main() {
	cli.Execute()
}
