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
	Routing     RoutingConfig  `yaml:"routing,omitempty"`      // 智能路由引擎配置
	MetricsAddr string         `yaml:"metrics_addr,omitempty"` // Prometheus 指标监听地址，例如 "127.0.0.1:9090"
	DNS         DNSConfig      `yaml:"dns,omitempty"`          // DNS 缓存与代理功能配置
}

// DNSConfig 代表 DNS 服务器相关的配置
type DNSConfig struct {
	Enable     bool   `yaml:"enable"`
	ListenAddr string `yaml:"listen_addr"`
	Upstream   string `yaml:"upstream"`
}

// RoutingConfig 代表智能路由的配置
type RoutingConfig struct {
	Mode         string `yaml:"mode"`          // 路由模式: "global", "direct", "pac"
	BypassLAN    bool   `yaml:"bypass_lan"`    // 是否直连局域网 IP
	ChnroutesURL string `yaml:"chnroutes_url"` // pac 模式下 chnroutes 的下载地址
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
	PoolSize  int    `yaml:"pool_size,omitempty"` // 连接池大小 (针对国际高延迟网络，建议 4-8)
	HostKey   string `yaml:"host_key,omitempty"`  // 预置主机密钥 SHA256 指纹（无人值守 TOFU 信任，格式 "SHA256:..."）
}

// TunnelConfig 代表一条隧道规则的具体配置
type TunnelConfig struct {
	Name        string   `yaml:"name"`
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

	// 自动解密被标记为 Encrypted 的服务器密码。
	// 密文密码必须通过 GOTUN_SECRET_KEY 提供密钥，否则报错退出。
	secretKey := os.Getenv("GOTUN_SECRET_KEY")

	for i, srv := range cfg.Servers {
		if srv.Encrypted && srv.Password != "" {
			if secretKey == "" {
				return nil, fmt.Errorf("server '%s': 密码为密文但未设置 GOTUN_SECRET_KEY 环境变量", srv.Name)
			}
			decrypted, err := crypto.Decrypt(srv.Password, secretKey)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt password for server '%s': %w", srv.Name, err)
			}
			cfg.Servers[i].Password = decrypted
		}
	}

	return &cfg, nil
}
