package genyaml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_读一对component与assembly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "erp", "sales")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	componentYAML := `
apiVersion: brickkit/v1
kind: Component
metadata:
  id: erp/sales
  version: 1.0.0
dependencies:
  components:
    - mdm/customer@1.0.0
    - id: infra/workflow@1.0.0
      optional: true
  resources:
    - { kind: database, engine: postgresql }
    - { kind: mq, engine: nats }
deployment:
  labels:
    prometheus.io/scrape: "true"
`
	assemblyYAML := `
id: erp/sales
version: 1.0.0
asset:
  assembly_role: optional
domain: erp
shell: go-core
data:
  schema: erp_sales
  role: erp_sales_rw
`
	if err := os.WriteFile(filepath.Join(dir, "component.yaml"), []byte(componentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assembly.yaml"), []byte(assemblyYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	specs, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("期望 1 个组件，得到 %d", len(specs))
	}
	s := specs[0]
	if s.ID != "erp/sales" || s.Version != "1.0.0" {
		t.Fatalf("id/version 解析不对：%+v", s)
	}
	if s.AssemblyRole != "optional" || s.Shell != "go-core" {
		t.Fatalf("assembly_role/shell 解析不对：%+v", s)
	}
	if !hasResource(s.Resources, "database", "postgresql") {
		t.Fatalf("应该识别出声明了 database/postgresql 资源，实际：%+v", s.Resources)
	}
	if !hasResource(s.Resources, "mq", "nats") {
		t.Fatalf("应该识别出声明了 mq/nats 资源，实际：%+v", s.Resources)
	}
	if len(s.Dependencies) != 2 {
		t.Fatalf("期望 2 条依赖，得到 %d：%+v", len(s.Dependencies), s.Dependencies)
	}
	foundOptional := false
	for _, d := range s.Dependencies {
		if d.ID == "infra/workflow" && d.Optional {
			foundOptional = true
		}
	}
	if !foundOptional {
		t.Fatalf("infra/workflow 应该被解析成 optional 依赖：%+v", s.Dependencies)
	}
	if s.Labels["prometheus.io/scrape"] != "true" {
		t.Fatalf("labels 解析不对：%+v", s.Labels)
	}
}

func hasResource(resources []ResourceDep, kind, engine string) bool {
	for _, r := range resources {
		if r.Kind == kind && r.Engine == engine {
			return true
		}
	}
	return false
}

// TestLoad_声明了shell的组件自动算出Local与LocalPort 是阶段四产出 4
// 依赖的核心行为：local:true 的 localPort 必须直接复用组件自己单跑时
// 声明的端口，不是另外分配一个（设计书 §13.8.1）——这条逻辑此前完全
// 没有接上（ComponentSpec.Local/LocalPort 只在测试里手工赋值过，
// loadOne 从没有据此推导过），Task 4 写产出 4 时才补上。
func TestLoad_声明了shell的组件自动算出Local与LocalPort(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mdm", "customer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	componentYAML := `
metadata:
  id: mdm/customer
  version: 1.0.5
deployment:
  port: 8080
  extraPorts:
    - { name: grpc, port: 9090 }
`
	assemblyYAML := `
id: mdm/customer
version: 1.0.5
shell: go-shell-core
data:
  schema: mdm_customer
  role: mdm_customer_rw
`
	if err := os.WriteFile(filepath.Join(dir, "component.yaml"), []byte(componentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assembly.yaml"), []byte(assemblyYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	specs, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("期望 1 个组件，得到 %d", len(specs))
	}
	s := specs[0]
	if !s.Local {
		t.Fatalf("声明了 shell 的组件应该自动 Local=true，实际：%+v", s)
	}
	if s.LocalPort != 8080 {
		t.Fatalf("LocalPort 应该直接复用 deployment.port（8080），实际 %d", s.LocalPort)
	}
	if s.Port != 8080 {
		t.Fatalf("Port 应该原样保留 deployment.port，实际 %d", s.Port)
	}
	if s.ExtraPorts["grpc"] != 9090 {
		t.Fatalf("ExtraPorts 解析不对：%+v", s.ExtraPorts)
	}
	if s.Schema != "mdm_customer" || s.Role != "mdm_customer_rw" {
		t.Fatalf("Schema/Role 解析不对：schema=%q role=%q", s.Schema, s.Role)
	}
}

// TestLoad_没声明shell的组件不会被误判成合并部署 是上一条的反面：
// 大多数组件在阶段四不会被合并（TS/前端、还没轮到的 Go/Python 组件），
// Local 必须保持 false，不能因为 loadOne 新增的推导逻辑而意外变成 true。
func TestLoad_没声明shell的组件不会被误判成合并部署(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "infra", "bff-mobile")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	componentYAML := `
metadata:
  id: infra/bff-mobile
  version: 1.0.13
deployment:
  port: 8500
`
	assemblyYAML := `
id: infra/bff-mobile
version: 1.0.13
`
	if err := os.WriteFile(filepath.Join(dir, "component.yaml"), []byte(componentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assembly.yaml"), []byte(assemblyYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	specs, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Local {
		t.Fatalf("没声明 shell 的组件不应该被判成 Local=true：%+v", specs[0])
	}
	if specs[0].LocalPort != 0 {
		t.Fatalf("没声明 shell 时 LocalPort 应该保持零值，实际 %d", specs[0].LocalPort)
	}
}

func TestLoad_目录为空返回空列表不报错(t *testing.T) {
	specs, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 {
		t.Fatalf("空目录应该返回空列表，得到 %d", len(specs))
	}
}
