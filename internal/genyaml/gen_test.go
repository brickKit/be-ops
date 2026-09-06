package genyaml

import (
	"strings"
	"testing"
)

func spec(id, version, role string) ComponentSpec {
	return ComponentSpec{ID: id, Version: version, AssemblyRole: role, Shell: "go-core"}
}

func TestGen_labels值必须是字符串(t *testing.T) {
	s := spec("erp/sales", "1.0.0", "optional")
	s.Labels = map[string]any{
		"prometheus.io/scrape": true,
		"prometheus.io/port":   8084,
	}
	out, err := Gen([]ComponentSpec{s}, []string{"erp/sales"})
	if err != nil {
		t.Fatal(err)
	}
	// 平台不做自动转换：布尔与数字都必须带引号产出
	for _, want := range []string{`prometheus.io/scrape: "true"`, `prometheus.io/port: "8084"`} {
		if !strings.Contains(out, want) {
			t.Errorf("产出里缺少 %q\n完整产出：\n%s", want, out)
		}
	}
}

func TestGen_没买的组件整条不写(t *testing.T) {
	specs := []ComponentSpec{
		spec("mdm/customer", "1.0.0", "default"),
		spec("hrm/attendance", "1.0.0", "optional"),
	}
	// 只启用 mdm/customer，不装 hrm
	out, err := Gen(specs, []string{"mdm/customer"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hrm/attendance") {
		t.Errorf("没买的组件不该整条出现，实际：\n%s", out)
	}
	if strings.Contains(out, "enabled: false") {
		t.Error("没买的组件是整条不写，不是 enabled: false（那会级联关掉下游）")
	}
	if !strings.Contains(out, "mdm/customer") {
		t.Error("买了的组件应该出现")
	}
}

func TestGen_聚合型组件依赖全optional(t *testing.T) {
	s := spec("infra/bff-mobile", "1.0.0", "default")
	s.Dependencies = []Dependency{
		{ID: "mdm/customer", Optional: false}, // ⚠️ 聚合型组件写了强依赖——这是要被拦的
	}
	_, err := Gen([]ComponentSpec{s, spec("mdm/customer", "1.0.0", "default")},
		[]string{"infra/bff-mobile", "mdm/customer"})
	if err == nil {
		t.Fatal("infra/bff-mobile 的依赖必须全部 optional，强依赖应该报错")
	}
}

func TestGen_slot互斥(t *testing.T) {
	a := spec("infra/iam-casdoor", "1.0.0", "slot")
	a.SlotName = "iam"
	b := spec("infra/iam-keycloak", "1.0.0", "slot")
	b.SlotName = "iam"
	_, err := Gen([]ComponentSpec{a, b}, []string{"infra/iam-casdoor", "infra/iam-keycloak"})
	if err == nil {
		t.Fatal("同一 slot_name 出现两个实现应该报错")
	}
}

func TestGen_channel多选放行(t *testing.T) {
	a := spec("integration/im-dingtalk", "1.0.0", "channel:im")
	b := spec("integration/im-feishu", "1.0.0", "channel:im")
	out, err := Gen([]ComponentSpec{a, b}, []string{"integration/im-dingtalk", "integration/im-feishu"})
	if err != nil {
		t.Fatalf("同一 channel 多个实现应该正常产出，实际报错：%v", err)
	}
	if !strings.Contains(out, "im-dingtalk") || !strings.Contains(out, "im-feishu") {
		t.Errorf("两个 channel 实现都该出现，实际：\n%s", out)
	}
}

func TestGen_精确版本(t *testing.T) {
	for _, bad := range []string{"^1.2", "~1.2.0", "1.2.x", "latest"} {
		_, err := Gen([]ComponentSpec{spec("mdm/customer", bad, "default")}, []string{"mdm/customer"})
		if err == nil {
			t.Errorf("version=%q 应该报错（范围版本不许出现）", bad)
		}
	}
}

func TestGen_local注释(t *testing.T) {
	s := spec("mdm/customer", "1.0.0", "default")
	s.Local = true
	s.LocalPort = 8080
	out, err := Gen([]ComponentSpec{s}, []string{"mdm/customer"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "local: true") {
		t.Fatal("local: true 应该出现")
	}
	// 每一串 local: true 上方必须有注释说明"这不是有人在调试，是合并部署"
	idx := strings.Index(out, "local: true")
	before := out[:idx]
	lastLine := before[strings.LastIndex(before[:len(before)-1], "\n")+1:]
	if !strings.HasPrefix(strings.TrimSpace(lastLine), "#") {
		t.Errorf("local: true 上一行应该是注释，实际上一行：%q", lastLine)
	}
}

// ⚠️ 补的测试，不在计划原文的 7 条里：声明了资源依赖却没有 bindings，
// brickkit up 会直接阻断（`006` §4.4，总纲产出 2b）。62 个组件手写
// bindings 必然漏，genyaml 必须自动把每个组件的 componentId 挂到它
// 所属外壳的那一条 PostgreSQL 资源条目下。
func TestGen_声明数据库资源的组件自动挂上bindings(t *testing.T) {
	s := spec("erp/sales", "1.0.0", "optional")
	s.Resources = []ResourceDep{{Kind: "database", Engine: "postgresql"}}
	out, err := Gen([]ComponentSpec{s}, []string{"erp/sales"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "componentId: erp/sales") {
		t.Errorf("声明了数据库资源却没有自动生成 bindings，产出：\n%s", out)
	}
}

// ⚠️ 实测踩坑：对着真实的 mdm-customer 组件跑 be-ops gen 时发现，genyaml
// 只自动生成过 database 的 bindings——mq/nats 那条资源依赖被漏掉了，
// brickkit up 因此仍然报"resources 中未声明"。产出 2b 的要求是"声明了
// 资源依赖的组件都要有 bindings"，不是"只有数据库"，这里补上非数据库
// 资源的自动绑定。
func TestGen_声明非数据库资源的组件也自动挂上bindings(t *testing.T) {
	s := spec("mdm/customer", "1.0.0", "default")
	s.Resources = []ResourceDep{
		{Kind: "database", Engine: "postgresql"},
		{Kind: "mq", Engine: "nats"},
	}
	out, err := Gen([]ComponentSpec{s}, []string{"mdm/customer"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kind: mq") || !strings.Contains(out, "engine: nats") {
		t.Errorf("应该产出 mq/nats 资源条目，产出：\n%s", out)
	}
	// mq 条目下也要挂 componentId——不能只有 kind/engine 没有 bindings，
	// 否则和产出 2b 要拦的问题一模一样，只是换了个资源种类
	idx := strings.Index(out, "kind: mq")
	if idx < 0 || !strings.Contains(out[idx:], "componentId: mdm/customer") {
		t.Errorf("mq 资源条目下应该有 mdm/customer 的 binding，产出：\n%s", out)
	}
}

// mq（以及未来任何非 database 资源）不像 PostgreSQL 那样每个外壳有不同
// 登录凭据（决策 3 只对数据库成立），所以不该按 Shell 拆成多条资源
// 条目——两个不同外壳的组件用同一个 mq 资源条目共享 bindings 即可。
func TestGen_mq资源不按外壳拆分(t *testing.T) {
	a := spec("mdm/customer", "1.0.0", "default")
	a.Shell = "go-core"
	a.Resources = []ResourceDep{{Kind: "mq", Engine: "nats"}}
	b := spec("infra/print", "1.0.0", "default")
	b.Shell = "py-render"
	b.Resources = []ResourceDep{{Kind: "mq", Engine: "nats"}}

	out, err := Gen([]ComponentSpec{a, b}, []string{"mdm/customer", "infra/print"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "kind: mq") != 1 {
		t.Errorf("不同外壳的组件共用同一个 mq 资源条目，应该只出现一次 kind: mq，产出：\n%s", out)
	}
	if !strings.Contains(out, "componentId: mdm/customer") || !strings.Contains(out, "componentId: infra/print") {
		t.Errorf("两个组件都应该出现在这唯一一条 mq 资源的 bindings 里，产出：\n%s", out)
	}
}
