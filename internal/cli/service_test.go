package cli

import (
	"testing"
)

// TestProgramStartFailsOnBadConfig 验证缺陷 C 修复：配置加载失败时 Start 返回 error，
// 而不是像旧实现那样静默返回 nil（导致服务管理器误以为启动成功）
func TestProgramStartFailsOnBadConfig(t *testing.T) {
	oldPath := svcConfigPath
	defer func() { svcConfigPath = oldPath }()

	svcConfigPath = t.TempDir() + "/nonexistent.yaml"
	p := &program{}
	if err := p.Start(nil); err == nil {
		t.Fatal("配置文件不存在时 Start 应返回错误")
	}
}

// TestProgramStopBeforeStart 验证尚未完成初始化时 Stop 安全返回
func TestProgramStopBeforeStart(t *testing.T) {
	p := &program{}
	if err := p.Stop(nil); err != nil {
		t.Fatalf("未初始化时 Stop 应安全返回 nil，得到: %v", err)
	}
}
