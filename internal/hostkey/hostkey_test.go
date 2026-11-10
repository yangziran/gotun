package hostkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func dummyAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("93.184.216.34"), Port: 22}
}

func testKey(t *testing.T) (ssh.PublicKey, ssh.PublicKey) {
	t.Helper()
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	k1, _ := ssh.NewPublicKey(priv1.Public())
	k2, _ := ssh.NewPublicKey(priv2.Public())
	return k1, k2
}

func setupPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	t.Setenv("GOTUN_KNOWN_HOSTS", path)
	return path
}

// 首次连接（无 TTY、无预置指纹）必须明确失败，不得静默放行
func TestUnknownHostFailsWithoutTTYOrPreset(t *testing.T) {
	setupPath(t)
	key, _ := testKey(t)
	v, err := NewVerifier("example.com", 22, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Callback("example.com:22", dummyAddr(), key); err == nil {
		t.Fatal("未信任主机应明确失败")
	} else if !strings.Contains(err.Error(), "host_key") {
		t.Fatalf("错误信息应引导预置 host_key: %v", err)
	}
}

// 预置指纹匹配 → 信任并写入 known_hosts；之后按已记录放行
func TestPresetFingerprintTrustsAndPersists(t *testing.T) {
	path := setupPath(t)
	key, _ := testKey(t)
	fp := FingerprintSHA256(key)

	v, _ := NewVerifier("example.com", 22, fp)
	if err := v.Callback("example.com:22", dummyAddr(), key); err != nil {
		t.Fatalf("预置指纹匹配应放行: %v", err)
	}
	data, _ := os.ReadFile(path)
	if len(data) == 0 {
		t.Fatal("信任后应写入 known_hosts")
	}

	// 已有记录：不再依赖预置指纹也能通过
	v2, _ := NewVerifier("example.com", 22, "")
	if err := v2.Callback("example.com:22", dummyAddr(), key); err != nil {
		t.Fatalf("已记录主机应放行: %v", err)
	}
}

// 预置指纹不匹配 → 拒绝
func TestPresetFingerprintMismatchRejects(t *testing.T) {
	setupPath(t)
	key, _ := testKey(t)
	v, _ := NewVerifier("example.com", 22, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err := v.Callback("example.com:22", dummyAddr(), key); err == nil {
		t.Fatal("指纹不匹配应拒绝")
	}
}

// 已记录密钥发生变化 → 拒绝且绝不覆盖
func TestKeyChangeRejectsAndKeepsRecord(t *testing.T) {
	path := setupPath(t)
	key1, key2 := testKey(t)

	v, _ := NewVerifier("example.com", 22, FingerprintSHA256(key1))
	if err := v.Callback("example.com:22", dummyAddr(), key1); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	v2, _ := NewVerifier("example.com", 22, "")
	if err := v2.Callback("example.com:22", dummyAddr(), key2); err == nil {
		t.Fatal("密钥变更应拒绝")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("拒绝后 known_hosts 不得被修改")
	}
}

func TestFingerprintFormat(t *testing.T) {
	key, _ := testKey(t)
	fp := FingerprintSHA256(key)
	if !strings.HasPrefix(fp, "SHA256:") || len(fp) != len("SHA256:")+43 {
		t.Fatalf("指纹格式错误: %q", fp)
	}
}

func TestInvalidPresetFormat(t *testing.T) {
	setupPath(t)
	if _, err := NewVerifier("example.com", 22, "not-a-fingerprint"); err == nil {
		t.Fatal("非法指纹格式应报错")
	}
}
