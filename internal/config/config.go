package config

import (
	"fmt"
	"os"

	"github.com/yangziran/gotun/pkg/crypto"
	"gopkg.in/yaml.v3"
)

// Config 代表整个工具的配置树
type Config struct {
	Servers     []ServerConfig `yaml:"servers"`
	Tunnels     []TunnelConfig `yaml:"tunnels"`
	MetricsAddr string         `yaml:"metrics_addr,omitempty"` // Prometheus 指标监听地址，例如 "127.0.0.1:9090"
}

// ServerConfig 代表一台 SSH 服务器的连接信息
type ServerConfig struct {
	Name      string `yaml:"name"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	User      string `yaml:"user"`
	Password  string `yaml:"password,omitempty"`
	Encrypted bool   `yaml:"encrypted,omitempty"` // 指示 password 字段是否为密文
	KeyPath   string `yaml:"key_path,omitempty"`  // 私钥文件路径 (如果使用私钥登录)
	JumpHost  string `yaml:"jump_host,omitempty"` // 堡垒机名称 (通过该跳板机连接当前服务器)
}

// TunnelConfig 代表一条隧道规则的具体配置
type TunnelConfig struct {
	Name        string   `yaml:"name"`
	ServerName  string   `yaml:"server_name,omitempty"`  // 向后兼容旧字段
	ServerNames []string `yaml:"server_names,omitempty"` // 多目标负载均衡
	Type        string   `yaml:"type"`                   // 隧道类型: "local", "dynamic", 或 "remote"
	LocalAddr   string   `yaml:"local_addr"`             // 本地监听/绑定的地址, 例如: "127.0.0.1:8080"
	RemoteAddr  string   `yaml:"remote_addr,omitempty"`  // 远端目标/绑定的地址, 例如: "127.0.0.1:3306"
}

// LoadConfig 从指定的文件路径解析 YAML 配置文件并返回配置对象
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file error: %w", err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config yaml error: %w", err)
	}

	// 自动解密被标记为 Encrypted 的服务器密码
	secretKey := os.Getenv("GOTUN_SECRET_KEY")
	if secretKey == "" {
		secretKey = crypto.DefaultSecretKey
	}

	for i, srv := range cfg.Servers {
		if srv.Encrypted && srv.Password != "" {
			decrypted, err := crypto.Decrypt(srv.Password, secretKey)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt password for server '%s': %w", srv.Name, err)
			}
			cfg.Servers[i].Password = decrypted
		}
	}

	// 兼容处理 server_name 和 server_names
	for i, tun := range cfg.Tunnels {
		if tun.ServerName != "" && len(tun.ServerNames) == 0 {
			cfg.Tunnels[i].ServerNames = []string{tun.ServerName}
		}
	}

	return &cfg, nil
}
