package shellconfig

import (
	"testing"

	"github.com/brickKit/be-ops/internal/genyaml"
)

func mod(id, shell string, port int, deps ...string) genyaml.ComponentSpec {
	s := genyaml.ComponentSpec{ID: id, Version: "1.0.0", Shell: shell, Port: port, Schema: id}
	for _, d := range deps {
		s.Dependencies = append(s.Dependencies, genyaml.Dependency{ID: d})
	}
	return s
}

// TestGen_按Shell字段分组_没声明的不出现在任何外壳里 是产出 4 最基础的
// 一条断言：只有非空 Shell 的组件才进外壳，TS/前端这类不合并的组件
// 完全不出现。
func TestGen_按Shell字段分组_没声明的不出现在任何外壳里(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("mdm/customer", "go-shell-core", 8080),
		mod("infra/print", "py-shell-render", 8400),
		mod("infra/bff-mobile", "", 8500), // 没声明 shell，不合并
	}

	shells, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(shells) != 2 {
		t.Fatalf("期望 2 个外壳（go-shell-core / py-shell-render），得到 %d：%+v", len(shells), shells)
	}
	for _, sh := range shells {
		for _, m := range sh.Modules {
			if m.ComponentID == "infra/bff-mobile" {
				t.Fatalf("没声明 shell 的组件不应该出现在任何外壳里")
			}
		}
	}
}

// TestGen_外壳内部按依赖顺序排序_被依赖方在前 是产出 4 的核心断言：
// erp-sales 依赖 mdm-customer，迁移/启动顺序必须是 mdm-customer 先。
func TestGen_外壳内部按依赖顺序排序_被依赖方在前(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", 8084, "mdm/customer", "mdm/product"),
		mod("mdm/customer", "go-shell-core", 8080),
		mod("mdm/product", "go-shell-core", 8081),
	}

	shells, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(shells) != 1 {
		t.Fatalf("期望 1 个外壳，得到 %d", len(shells))
	}
	order := idsOf(shells[0].Modules)
	posCustomer, posProduct, posSales := indexOf(order, "mdm/customer"), indexOf(order, "mdm/product"), indexOf(order, "erp/sales")
	if posCustomer < 0 || posProduct < 0 || posSales < 0 {
		t.Fatalf("三个模块都应该出现在排序结果里，实际 %v", order)
	}
	if posSales < posCustomer || posSales < posProduct {
		t.Fatalf("erp/sales 依赖 mdm/customer 与 mdm/product，必须排在它们后面，实际顺序 %v", order)
	}
}

// TestGen_跨外壳依赖不影响外壳内部排序 是四外壳拓扑发现之后新增的
// 断言：crm-opportunity（go-shell-backoffice）强依赖 mdm-customer/
// mdm-product（go-shell-core），这条边跨外壳，不该让 topoSort 去
// 同一个外壳里找一个根本不在这里的依赖，也不该报错。
func TestGen_跨外壳依赖不影响外壳内部排序(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("crm/opportunity", "go-shell-backoffice", 8100, "mdm/customer", "mdm/product"),
		mod("mdm/customer", "go-shell-core", 8080),
		mod("mdm/product", "go-shell-core", 8081),
	}

	shells, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(shells) != 2 {
		t.Fatalf("期望 2 个外壳，得到 %d：%+v", len(shells), shells)
	}
	for _, sh := range shells {
		if sh.Name == "go-shell-backoffice" {
			if len(sh.Modules) != 1 || sh.Modules[0].ComponentID != "crm/opportunity" {
				t.Fatalf("go-shell-backoffice 应该只有 crm/opportunity 一个模块，实际 %+v", sh.Modules)
			}
		}
	}
}

// TestGen_环形依赖报错不静默丢弃 防的是"生成器自己写错，产出一份看起来
// 正常但顺序不对的清单"——这种情况不该出现，必须报错。
func TestGen_环形依赖报错不静默丢弃(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("a/a", "go-shell-core", 8080, "a/b"),
		mod("a/b", "go-shell-core", 8081, "a/a"),
	}
	if _, err := Gen(specs); err == nil {
		t.Fatal("期望环形依赖报错，实际没有")
	}
}

// TestGen_产出确定性_同样输入多次生成顺序一致 防的是"map 遍历顺序不同，
// 每次跑出来的合并清单文件 diff 一大片"——生成器必须是确定性的。
func TestGen_产出确定性_同样输入多次生成顺序一致(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("infra/notification", "go-shell-infra", 8201),
		mod("infra/workflow", "go-shell-infra", 8202),
		mod("infra/authz", "go-shell-infra", 8203),
		mod("infra/iam-casdoor", "go-shell-infra", 8204),
		mod("integration/im-dingtalk", "go-shell-infra", 8205),
	}
	first, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := Gen(specs)
		if err != nil {
			t.Fatal(err)
		}
		if !sameOrder(idsOf(got[0].Modules), idsOf(first[0].Modules)) {
			t.Fatalf("第 %d 次生成的顺序与第一次不一致：%v vs %v", i, idsOf(got[0].Modules), idsOf(first[0].Modules))
		}
	}
}

// TestGen_模块字段原样转发端口与schema 确认产出 4 不发明新的端口/
// schema 来源，原样转发 genyaml.Load 已经读出来的值。
func TestGen_模块字段原样转发端口与schema(t *testing.T) {
	s := mod("mdm/customer", "go-shell-core", 8080)
	s.ExtraPorts = map[string]int{"grpc": 9090}
	s.Schema = "mdm_customer"

	shells, err := Gen([]genyaml.ComponentSpec{s})
	if err != nil {
		t.Fatal(err)
	}
	m := shells[0].Modules[0]
	if m.HTTPPort != 8080 || m.ExtraPorts["grpc"] != 9090 || m.Schema != "mdm_customer" {
		t.Fatalf("字段没有原样转发：%+v", m)
	}
}

// TestGen_模块字段原样转发Config 是阶段四附加 Task 0.2 新增的断言：
// Config 是调用方（cmd/be-ops/shell.go）用 genyaml.MergeConfig 算好的
// 最终结果，本包只原样转发，不重新合并、不丢字段。
func TestGen_模块字段原样转发Config(t *testing.T) {
	s := mod("mdm/customer", "go-shell-core", 8080)
	s.Config = map[string]string{"pgSchema": "mdm_customer", "otelBaseUrl": ""}

	shells, err := Gen([]genyaml.ComponentSpec{s})
	if err != nil {
		t.Fatal(err)
	}
	m := shells[0].Modules[0]
	if m.Config["pgSchema"] != "mdm_customer" {
		t.Fatalf("Config 没有原样转发：%+v", m.Config)
	}
	if v, ok := m.Config["otelBaseUrl"]; !ok || v != "" {
		t.Fatalf("Config 里写了空字符串的 key 也应该保留，实际 ok=%v v=%q", ok, v)
	}
}

func idsOf(modules []Module) []string {
	out := make([]string, len(modules))
	for i, m := range modules {
		out[i] = m.ComponentID
	}
	return out
}

func indexOf(ids []string, id string) int {
	for i, x := range ids {
		if x == id {
			return i
		}
	}
	return -1
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
