//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func getCloseSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM}
}

func getReloadSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}

func isReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}

func sendReloadSignal(process *os.Process) error {
	return process.Signal(syscall.SIGHUP)
}
