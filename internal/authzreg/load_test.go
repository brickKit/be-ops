package authzreg

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAssemblyDecls_读一个组件的权限与数据权限声明(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "erp/sales/assembly.yaml"), `
id: erp/sales
permissions:
  - { key: erp.sales.view,   title: 查看销售订单, type: page   }
  - { key: erp.sales.create, title: 建立订单,     type: action }
data_scopes:
  - { dimension: org,   column: dept_path, mode: prefix, tables: [sales_orders] }
  - { dimension: owner, column: owner_id,  mode: equals, tables: [sales_orders] }
`)
	decls, err := LoadAssemblyDecls(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 1 {
		t.Fatalf("期望 1 个组件，得到 %d", len(decls))
	}
	d := decls[0]
	if d.ComponentID != "erp/sales" {
		t.Fatalf("component id 不对：%+v", d)
	}
	if len(d.Permissions) != 2 || d.Permissions[0].Key != "erp.sales.view" {
		t.Fatalf("permissions 解析不对：%+v", d.Permissions)
	}
	if d.DataScopesNone {
		t.Fatal("这个组件声明了真实维度，不该被判成 none")
	}
	if len(d.DataScopes) != 2 || d.DataScopes[0].Dimension != "org" {
		t.Fatalf("data_scopes 解析不对：%+v", d.DataScopes)
	}
	if len(d.DataScopes[0].Tables) != 1 || d.DataScopes[0].Tables[0] != "sales_orders" {
		t.Fatalf("tables 解析不对：%+v", d.DataScopes[0])
	}
}

func TestLoadAssemblyDecls_data_scopes写none(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "mdm/customer/assembly.yaml"), `
id: mdm/customer
permissions:
  - { key: mdm.customer.view, title: 查看客户主数据, type: page }
data_scopes: none
`)
	decls, err := LoadAssemblyDecls(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 1 {
		t.Fatalf("期望 1 个组件，得到 %d", len(decls))
	}
	if !decls[0].DataScopesNone {
		t.Fatal("data_scopes: none 应该被识别成 DataScopesNone=true")
	}
	if len(decls[0].DataScopes) != 0 {
		t.Fatalf("none 时不该有任何维度，得到 %+v", decls[0].DataScopes)
	}
}

// TestLoadAssemblyDecls_省略data_scopes段当场报错 是导读第22条：安全机制
// 的默认值只能 fail-closed，省略 ≠ none。
func TestLoadAssemblyDecls_省略data_scopes段当场报错(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "mdm/customer/assembly.yaml"), `
id: mdm/customer
permissions:
  - { key: mdm.customer.view, title: 查看客户主数据, type: page }
`)
	_, err := LoadAssemblyDecls(root)
	if err == nil {
		t.Fatal("省略 data_scopes 段应该报错，不该静默通过")
	}
}

// TestLoadAssemblyDecls_data_scopes写成其他标量也报错 确认唯一合法的
// 标量值是 "none"，不是随便写个字符串就当"不需要"。
func TestLoadAssemblyDecls_data_scopes写成其他标量也报错(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "mdm/customer/assembly.yaml"), `
id: mdm/customer
data_scopes: skip
`)
	_, err := LoadAssemblyDecls(root)
	if err == nil {
		t.Fatal("data_scopes 的标量值只能是 none，写别的应该报错")
	}
}

func TestLoadAssemblyDecls_目录为空返回空列表不报错(t *testing.T) {
	decls, err := LoadAssemblyDecls(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 0 {
		t.Fatalf("空目录应该返回空列表，得到 %d", len(decls))
	}
}

func TestLoadAssemblyDecls_没有assembly文件的目录被跳过(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "erp", "unfinished"), 0o755); err != nil {
		t.Fatal(err)
	}
	decls, err := LoadAssemblyDecls(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 0 {
		t.Fatalf("没有 assembly.yaml 的目录应该被跳过，得到 %d", len(decls))
	}
}
