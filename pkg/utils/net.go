package utils

import (
	"fmt"
	"net"
)

// GetDualStackAddrs 根据配置的主机地址，自动衍生双栈监听地址列表
func GetDualStackAddrs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{addr}
	}
	if host == "127.0.0.1" {
		return []string{addr, fmt.Sprintf("[::1]:%s", port)}
	}
	if host == "0.0.0.0" {
		return []string{addr, fmt.Sprintf("[::]:%s", port)}
	}
	return []string{addr}
}
