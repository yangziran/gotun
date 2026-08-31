//go:build windows

package cli

import (
	"fmt"
	"os"
)

func getCloseSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func getReloadSignals() []os.Signal {
	return []os.Signal{}
}

func isReloadSignal(sig os.Signal) bool {
	return false
}

func sendReloadSignal(process *os.Process) error {
	return fmt.Errorf("Windows 平台不支持 SIGHUP 信号热加载，请使用 'gotun service restart'")
}
