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
	if !s.NeedsDatabase {
		t.Fatal("应该识别出声明了 database 资源")
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

func TestLoad_目录为空返回空列表不报错(t *testing.T) {
	specs, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 {
		t.Fatalf("空目录应该返回空列表，得到 %d", len(specs))
	}
}
