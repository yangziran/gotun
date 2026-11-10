// Package hostkey 实现 SSH 主机密钥的 TOFU（Trust On First Use）校验，
// 替代 ssh.InsecureIgnoreHostKey。三种信任语义：
//  1. known_hosts 中已有记录且一致 → 放行；
//  2. 无记录 → 预置 host_key 指纹匹配则信任并持久化；或 TTY 交互确认；否则明确失败；
//  3. 有记录但密钥变化 → 拒绝连接，绝不覆盖（防中间人）。
package hostkey

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"
)

var (
	fileMu   sync.Mutex // 保护 known_hosts 追加写入
	promptMu sync.Mutex // 串行化 TTY 交互确认（并发拨号时避免提示交错）
)

// DefaultPath 返回 known_hosts 路径：优先环境变量 GOTUN_KNOWN_HOSTS（系统服务场景请显式指定），
// 默认 ~/.ssh/known_hosts（与 OpenSSH 格式互通）。
func DefaultPath() string {
	if p := os.Getenv("GOTUN_KNOWN_HOSTS"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// FingerprintSHA256 计算主机密钥的 SHA256 指纹（与 ssh-keygen -lf 输出格式一致）
func FingerprintSHA256(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Verifier 校验单台服务器的主机密钥
type Verifier struct {
	path     string
	addr     string // host:port（known_hosts 键）
	presetFP string // 预置指纹 "SHA256:..."（无人值守信任），可为空
}

// NewVerifier 创建校验器。host/port 为目标服务器；presetFP 为可选的预置 SHA256 指纹。
func NewVerifier(host string, port int, presetFP string) (*Verifier, error) {
	if presetFP != "" && !strings.HasPrefix(presetFP, "SHA256:") {
		return nil, fmt.Errorf("host_key 指纹格式应为 \"SHA256:...\"，实际: %q", presetFP)
	}
	path := DefaultPath()
	if path == "" {
		return nil, errors.New("无法确定 known_hosts 路径，请设置 GOTUN_KNOWN_HOSTS 环境变量")
	}
	return &Verifier{
		path:     path,
		addr:     net.JoinHostPort(host, strconv.Itoa(port)),
		presetFP: presetFP,
	}, nil
}

// Callback 实现 ssh.HostKeyCallback。跳板与最终节点各自独立构造 Verifier，独立校验。
func (v *Verifier) Callback(_ string, remote net.Addr, key ssh.PublicKey) error {
	fp := FingerprintSHA256(key)

	cb, err := v.knownHostsCallback()
	if err != nil {
		return fmt.Errorf("初始化 known_hosts 失败: %w", err)
	}

	err = cb(v.addr, remote, key)
	if err == nil {
		return nil // 已信任
	}

	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		if len(keyErr.Want) > 0 {
			// 已有记录但密钥变化：拒绝，绝不覆盖
			return fmt.Errorf("主机密钥变更！可能是中间人攻击。known_hosts 记录 %q，实际 %q。请人工核实后再删除旧记录",
				keyErr.Want[0].Key.Type(), key.Type())
		}
		// 未知主机：TOFU 决策
		return v.decide(key, fp)
	}
	return err
}

// knownHostsCallback 读取 known_hosts；文件不存在时创建空文件（首次使用）
func (v *Verifier) knownHostsCallback() (ssh.HostKeyCallback, error) {
	if _, err := os.Stat(v.path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(v.path, os.O_CREATE, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	}
	return knownhosts.New(v.path)
}

// decide 处理首次连接的信任决策：预置指纹 → TTY 交互 → 明确失败
func (v *Verifier) decide(key ssh.PublicKey, fp string) error {
	// 1. 预置指纹（服务部署/无人值守）
	if v.presetFP != "" {
		if v.presetFP == fp {
			return v.trust(key)
		}
		return fmt.Errorf("预置 host_key 指纹不匹配：配置 %q，实际 %q，拒绝连接", v.presetFP, fp)
	}

	// 2. TTY 交互确认
	if term.IsTerminal(int(os.Stdin.Fd())) {
		promptMu.Lock()
		defer promptMu.Unlock()
		fmt.Fprintf(os.Stderr, "\n首次连接到 %s。\n密钥类型: %s\nSHA256 指纹: %s\n请核对后输入 y 信任，其它输入拒绝 [y/N]: ", v.addr, key.Type(), fp)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(answer), "y") {
			return v.trust(key)
		}
		return fmt.Errorf("用户拒绝信任主机 %s", v.addr)
	}

	// 3. 无人值守且无预置指纹：明确失败（不留静默放行路径）
	return fmt.Errorf("未信任的主机 %s（%s %s）。无人值守模式请在服务器配置中预置 host_key: %q（可用 ssh-keyscan 获取）",
		v.addr, key.Type(), fp, fp)
}

// trust 将密钥以 OpenSSH 兼容格式追加到 known_hosts
func (v *Verifier) trust(key ssh.PublicKey) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := os.OpenFile(v.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入 known_hosts 失败: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(knownhosts.Line([]string{v.addr}, key) + "\n"); err != nil {
		return fmt.Errorf("写入 known_hosts 失败: %w", err)
	}
	return nil
}
