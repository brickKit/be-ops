package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 阶段四附加 Task 0.4：runShellConfig 新增 "--shell <name>" 用法——只打印
// 一个外壳的 modules 数组（compact JSON），不写文件，供人工粘贴进
// brickkit.yaml 的 config.shellConfigJson（servedBy 外壳没有 volumes 可以
// 挂载文件，只能靠这条命令手动生成再写进配置字面量，见 shell.go 顶部注释）。

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "components", "mdm", "customer", "component.yaml"), `
metadata:
  id: mdm/customer
  version: 1.0.0
deployment:
  port: 8080
`)
	writeFile(t, filepath.Join(root, "components", "mdm", "customer", "assembly.yaml"), `
id: mdm/customer
version: 1.0.0
shell: go-core
data:
  schema: mdm_customer
  role: mdm_customer_rw
`)
	writeFile(t, filepath.Join(root, "brickkit.yaml"), `
components:
  - id: mdm/customer
    version: 1.0.0
`)
	return root
}

func TestRunShellConfig_按shell参数只打印一个外壳的modules(t *testing.T) {
	root := setupFixtureRepo(t)

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	err := runShellConfig([]string{"--root", root, "--shell", "go-core"})
	_ = w.Close()
	os.Stdout = stdout
	if err != nil {
		t.Fatalf("runShellConfig 失败: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, `"componentId":"mdm/customer"`) {
		t.Fatalf("输出里没有 mdm/customer: %s", out)
	}
	if !strings.Contains(out, `"httpPort":8080`) {
		t.Fatalf("输出里没有正确的端口: %s", out)
	}
}

func TestRunShellConfig_找不到外壳报错(t *testing.T) {
	root := setupFixtureRepo(t)
	if err := runShellConfig([]string{"--root", root, "--shell", "go-infra"}); err == nil {
		t.Fatal("外壳名找不到应该报错，实际没有")
	}
}

func TestRunShellConfig_out与shell都不给或都给时报错(t *testing.T) {
	root := setupFixtureRepo(t)
	if err := runShellConfig([]string{"--root", root}); err == nil {
		t.Fatal("--out 和 --shell 都不给应该报错，实际没有")
	}
	out := filepath.Join(t.TempDir(), "shell-config.json")
	if err := runShellConfig([]string{"--root", root, "--out", out, "--shell", "go-core"}); err == nil {
		t.Fatal("--out 和 --shell 同时给应该报错，实际没有")
	}
}
