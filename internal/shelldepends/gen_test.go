package shelldepends

import (
	"testing"

	"github.com/brickKit/be-ops/internal/genyaml"
)

func mod(id, shell string, deps ...genyaml.Dependency) genyaml.ComponentSpec {
	return genyaml.ComponentSpec{ID: id, Version: "1.0.0", Shell: shell, Dependencies: deps}
}

func strong(id string) genyaml.Dependency { return genyaml.Dependency{ID: id} }
func weak(id string) genyaml.Dependency   { return genyaml.Dependency{ID: id, Optional: true} }

// TestGen_跨外壳强依赖产出真实的depends_on 是本包最核心的一条断言：
// crm-opportunity（go-shell-backoffice）强依赖 mdm-customer/mdm-product
// （go-shell-core），go-shell-backoffice 必须 depends_on go-shell-core。
func TestGen_跨外壳强依赖产出真实的depends_on(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("crm/opportunity", "go-shell-backoffice", strong("mdm/customer"), strong("mdm/product")),
		mod("mdm/customer", "go-shell-core"),
		mod("mdm/product", "go-shell-core"),
	}

	out, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	deps := dependsOnOf(out, "go-shell-backoffice")
	if len(deps) != 1 || deps[0] != "go-shell-core" {
		t.Fatalf("期望 go-shell-backoffice 只依赖 go-shell-core，实际 %v", deps)
	}
	if len(dependsOnOf(out, "go-shell-core")) != 0 {
		t.Fatalf("go-shell-core 不应该有任何 dependsOn，实际 %v", dependsOnOf(out, "go-shell-core"))
	}
}

// TestGen_弱依赖不产生跨外壳启动顺序 是包注释里写的判据：erp-sales
// （go-shell-core）弱依赖 infra-workflow（go-shell-infra），缺失时
// 平台本来就容忍，不该被升级成一条启动阻塞边。
func TestGen_弱依赖不产生跨外壳启动顺序(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", weak("infra/workflow")),
		mod("infra/workflow", "go-shell-infra"),
	}

	out, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOnOf(out, "go-shell-core")) != 0 {
		t.Fatalf("弱依赖不该产生 dependsOn，实际 %v", dependsOnOf(out, "go-shell-core"))
	}
}

// TestGen_同外壳内部的依赖不出现在dependsOn里 防的是把产出 4 该管的
// 事情（外壳内部顺序）重复写进产出 8（外壳之间顺序）。
func TestGen_同外壳内部的依赖不出现在dependsOn里(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", strong("mdm/customer")),
		mod("mdm/customer", "go-shell-core"),
	}

	out, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOnOf(out, "go-shell-core")) != 0 {
		t.Fatalf("同外壳内部的依赖不该产生 dependsOn，实际 %v", dependsOnOf(out, "go-shell-core"))
	}
}

// TestGen_没合并的依赖方不产生dependsOn 是"依赖方还是独立容器、没有
// shell 字段"这种情况——不该报错，也不该产生任何 depends_on（那是
// brickKit 自己按正常 DNS 解决的事，不归外壳间顺序管）。
func TestGen_没合并的依赖方不产生dependsOn(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", strong("infra/bff-mobile")),
		mod("infra/bff-mobile", ""), // 没合并，保持独立容器
	}

	out, err := Gen(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependsOnOf(out, "go-shell-core")) != 0 {
		t.Fatalf("依赖方不合并部署时不该产生 dependsOn，实际 %v", dependsOnOf(out, "go-shell-core"))
	}
}

// TestGen_外壳级环形依赖报错 防的是"两个应该拆开的外壳被误分到了一起"
// 这类分组错误静默产出一份 Docker Compose 用不了的循环 depends_on。
func TestGen_外壳级环形依赖报错(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("a/a", "shell-a", strong("b/b")),
		mod("b/b", "shell-b", strong("a/a")),
	}
	if _, err := Gen(specs); err == nil {
		t.Fatal("期望外壳级环形依赖报错，实际没有")
	}
}

func dependsOnOf(all []ShellDeps, name string) []string {
	for _, s := range all {
		if s.Name == name {
			return s.DependsOn
		}
	}
	return nil
}
