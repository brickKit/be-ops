package authzreg

import "testing"

func TestGenPermissions_新键直接追加(t *testing.T) {
	rows, warnings, err := GenPermissions(nil, []AssemblyDecl{
		{ComponentID: "mdm/customer", Permissions: []PermissionDecl{
			{Key: "mdm.customer.view", Title: "查看客户主数据", Type: "page"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("首次生成不该有警告：%v", warnings)
	}
	if len(rows) != 1 || rows[0].Key != "mdm.customer.view" || rows[0].OwnerComponent != "mdm/customer" {
		t.Fatalf("产出不对：%+v", rows)
	}
	if rows[0].Deprecated != "" {
		t.Fatalf("新键不该带 deprecated：%+v", rows[0])
	}
}

// TestGenPermissions_已有键永远保留 是导读"四张钉死的表"的核心判据：
// 已发布的 key 不许因为这次扫描没有它就被删掉——废弃走 deprecated
// 墓碑列，be-ops 自己不许悄悄删行。
func TestGenPermissions_已有键永远保留(t *testing.T) {
	existing := []PermissionRow{
		{Key: "mdm.customer.view", Title: "查看客户主数据", Type: "page", OwnerComponent: "mdm/customer"},
	}
	// 这次扫描里 mdm/customer 已经不再声明这个 key 了（比如组件被临时
	// 移出 components/）。
	rows, warnings, err := GenPermissions(existing, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "mdm.customer.view" {
		t.Fatalf("已发布的 key 应该被保留，得到 %+v", rows)
	}
	if len(warnings) != 1 {
		t.Fatalf("应该有 1 条孤儿 key 警告，得到 %v", warnings)
	}
}

// TestGenPermissions_已有键标了deprecated就不再警告 确认墓碑列生效后
// 孤儿检测不会一直吵。
func TestGenPermissions_已有键标了deprecated就不再警告(t *testing.T) {
	existing := []PermissionRow{
		{Key: "mdm.customer.old", OwnerComponent: "mdm/customer", Deprecated: "2026-01-01"},
	}
	rows, warnings, err := GenPermissions(existing, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("已标 deprecated 的 key 也该保留，得到 %+v", rows)
	}
	if len(warnings) != 0 {
		t.Fatalf("已标 deprecated 不该再警告，得到 %v", warnings)
	}
}

// TestGenPermissions_已有键刷新标题类型 确认"只增不改"保护的是 key
// 本身，不是它的展示文本——title/type 允许跟着 assembly.yaml 刷新。
func TestGenPermissions_已有键刷新标题类型(t *testing.T) {
	existing := []PermissionRow{
		{Key: "mdm.customer.view", Title: "旧标题", Type: "page", OwnerComponent: "mdm/customer", Deprecated: ""},
	}
	rows, _, err := GenPermissions(existing, []AssemblyDecl{
		{ComponentID: "mdm/customer", Permissions: []PermissionDecl{
			{Key: "mdm.customer.view", Title: "新标题", Type: "page"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Title != "新标题" {
		t.Fatalf("title 应该刷新成最新声明，得到 %+v", rows[0])
	}
}

// TestGenPermissions_同一个key被两个组件声明报错 权限键必须全局唯一
// （registry/permissions.tsv 的 key 是跨组件的持久标识）。
func TestGenPermissions_同一个key被两个组件声明报错(t *testing.T) {
	_, _, err := GenPermissions(nil, []AssemblyDecl{
		{ComponentID: "mdm/customer", Permissions: []PermissionDecl{{Key: "shared.key"}}},
		{ComponentID: "erp/sales", Permissions: []PermissionDecl{{Key: "shared.key"}}},
	})
	if err == nil {
		t.Fatal("同一个 key 被两个组件声明应该报错")
	}
}

func TestGenPermissions_按key排序(t *testing.T) {
	rows, _, err := GenPermissions(nil, []AssemblyDecl{
		{ComponentID: "erp/sales", Permissions: []PermissionDecl{{Key: "erp.sales.view"}}},
		{ComponentID: "mdm/customer", Permissions: []PermissionDecl{{Key: "mdm.customer.view"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Key != "erp.sales.view" || rows[1].Key != "mdm.customer.view" {
		t.Fatalf("应该按 key 字典序排序，得到 %+v", rows)
	}
}

func TestGenDataScopes_none不产生行(t *testing.T) {
	rows := GenDataScopes([]AssemblyDecl{
		{ComponentID: "mdm/customer", DataScopesNone: true},
	})
	if len(rows) != 0 {
		t.Fatalf("data_scopes: none 不该产生任何行，得到 %+v", rows)
	}
}

func TestGenDataScopes_每个维度一行且tables逗号分隔(t *testing.T) {
	rows := GenDataScopes([]AssemblyDecl{
		{ComponentID: "erp/sales", DataScopes: []DataScopeDecl{
			{Dimension: "org", Column: "dept_path", Mode: "prefix", Tables: []string{"sales_orders"}},
			{Dimension: "owner", Column: "owner_id", Mode: "equals", Tables: []string{"sales_orders", "sales_order_lines"}},
		}},
	})
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，得到 %d：%+v", len(rows), rows)
	}
	if rows[0].Component != "erp/sales" || rows[0].Dimension != "org" {
		t.Fatalf("第一行不对：%+v", rows[0])
	}
	if rows[1].Tables != "sales_orders,sales_order_lines" {
		t.Fatalf("tables 应该逗号分隔，得到 %q", rows[1].Tables)
	}
}

func TestGenDataScopes_纯派生每次全量重生成(t *testing.T) {
	// registry/README.md：data-scopes.tsv 不需要防改，纯派生——不像
	// permissions.tsv 那样要跟已有内容合并，这里不接受 existing 参数。
	rows := GenDataScopes(nil)
	if len(rows) != 0 {
		t.Fatalf("空声明应该产出空表，得到 %+v", rows)
	}
}
