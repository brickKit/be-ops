package shellenv

import (
	"testing"

	"github.com/brickKit/be-ops/internal/genyaml"
)

// mod 造的组件默认 Local: true——本文件绝大多数用例测的是"这个外壳
// 已经原子式切换完，产出 7 该怎么改写"，不是切换时机本身。切换时机的
// 场景见 TestGen_外壳还没原子式切换完时跳过不报错，那条用例会显式传
// Local: false。
func mod(id, shell string, extraPorts map[string]int, deps ...string) genyaml.ComponentSpec {
	s := genyaml.ComponentSpec{ID: id, Version: "1.0.0", Shell: shell, ExtraPorts: extraPorts, Local: true}
	for _, d := range deps {
		s.Dependencies = append(s.Dependencies, genyaml.Dependency{ID: d})
	}
	return s
}

// TestGen_同外壳依赖改写成127001 是设计书 §13.8.2 规则的第一半：
// erp-sales 与 mdm-customer 都在 go-shell-core，brickKit 生成的
// localhost 地址本来就对，只需要把 "localhost" 换成 "127.0.0.1"
// （产出的是外壳启动器实际要读的最终值，不是"要不要改写"这件事本身）。
func TestGen_同外壳依赖改写成127001(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", nil, "mdm/customer"),
		mod("mdm/customer", "go-shell-core", nil),
	}
	localDebug := map[string]map[string]string{
		"erp/sales":    {"MDM_CUSTOMER_ENDPOINT": "http://localhost:8080"},
		"mdm/customer": {},
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatal(err)
	}
	got := moduleEnv(out, "go-shell-core", "erp/sales")["MDM_CUSTOMER_ENDPOINT"]
	if got != "http://127.0.0.1:8080" {
		t.Fatalf("期望 http://127.0.0.1:8080，实际 %q", got)
	}
}

// TestGen_跨外壳依赖改写成host_gateway 是 §13.8.2 规则的第二半，也是
// 四外壳拓扑发现之后新增的真实场景：crm-opportunity（go-shell-
// backoffice）依赖 mdm-customer（go-shell-core），brickKit 自己生成的
// localhost 地址在这种情况下是错的（指向 crm-opportunity 自己的进程），
// 必须换成宿主机地址。
func TestGen_跨外壳依赖改写成host_gateway(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("crm/opportunity", "go-shell-backoffice", nil, "mdm/customer"),
		mod("mdm/customer", "go-shell-core", nil),
	}
	localDebug := map[string]map[string]string{
		"crm/opportunity": {"MDM_CUSTOMER_ENDPOINT": "http://localhost:8080"},
		"mdm/customer":    {},
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatal(err)
	}
	got := moduleEnv(out, "go-shell-backoffice", "crm/opportunity")["MDM_CUSTOMER_ENDPOINT"]
	want := "http://" + HostGatewayAddr + ":8080"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestGen_依赖方不合并部署时原样透传 验证依赖方是真实独立容器（没有
// Shell）时，local-debug 给的正常 DNS 地址不会被误改写。
func TestGen_依赖方不合并部署时原样透传(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("erp/sales", "go-shell-core", nil, "infra/bff-mobile"),
		mod("infra/bff-mobile", "", nil), // 不合并，保持独立容器
	}
	localDebug := map[string]map[string]string{
		"erp/sales":        {"INFRA_BFF_MOBILE_ENDPOINT": "http://infra-bff-mobile-1-0-13:8500"},
		"infra/bff-mobile": {},
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatal(err)
	}
	got := moduleEnv(out, "go-shell-core", "erp/sales")["INFRA_BFF_MOBILE_ENDPOINT"]
	want := "http://infra-bff-mobile-1-0-13:8500"
	if got != want {
		t.Fatalf("期望原样透传 %q，实际 %q", want, got)
	}
}

// TestGen_额外端口的ENDPOINT也会被正确改写 验证 GRPC 这类 extraPorts
// 衍生出的 key（ERP_SALES_GRPC_ENDPOINT 这种命名）同样被识别、改写。
func TestGen_额外端口的ENDPOINT也会被正确改写(t *testing.T) {
	specs := []genyaml.ComponentSpec{
		mod("crm/opportunity", "go-shell-backoffice", nil, "erp/sales"),
		mod("erp/sales", "go-shell-core", map[string]int{"grpc": 9094}),
	}
	localDebug := map[string]map[string]string{
		"crm/opportunity": {"ERP_SALES_GRPC_ENDPOINT": "http://localhost:9094"},
		"erp/sales":       {},
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatal(err)
	}
	got := moduleEnv(out, "go-shell-backoffice", "crm/opportunity")["ERP_SALES_GRPC_ENDPOINT"]
	want := "http://" + HostGatewayAddr + ":9094"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}

// TestGen_非依赖地址的key原样保留 确认本包只动依赖地址这一类 key，
// 组件自己的 config 值（比如 configSchema 项）不受影响。
func TestGen_非依赖地址的key原样保留(t *testing.T) {
	specs := []genyaml.ComponentSpec{mod("mdm/customer", "go-shell-core", nil)}
	localDebug := map[string]map[string]string{
		"mdm/customer": {"COMPONENT_ID": "mdm/customer", "OTEL_BASE_URL": "http://localhost:4318"},
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatal(err)
	}
	env := moduleEnv(out, "go-shell-core", "mdm/customer")
	if env["COMPONENT_ID"] != "mdm/customer" {
		t.Fatalf("非依赖地址的 key 不应该被改写：%+v", env)
	}
	// OTEL_BASE_URL 恰好长得像 localhost:端口，但它不是任何依赖的
	// ENDPOINT key（没有组件叫这个名字），必须原样保留。
	if env["OTEL_BASE_URL"] != "http://localhost:4318" {
		t.Fatalf("非 ENDPOINT 类 key 被误改写：%+v", env)
	}
}

// TestGen_缺少local_debug数据时整个外壳静默跳过 覆盖两类此前会被
// 混为一谈的场景：①真的忘了先 brickkit up --dry-run；②这个外壳的
// assembly.yaml 早就声明了 shell 字段，但 brickkit.yaml 里原子式切换
// 成 local: true 是分任务做的（阶段四 Task 6 真机跑到的场景：Task 6
// 先切 3 个 Go 外壳，Task 7 才轮到 py-render），还没轮到的外壳自然没有
// local-debug 数据。两种情况从 `Gen` 的输入形状上根本无法区分——`Gen`
// 因此统一处理成"跳过这整个外壳，不报错"，不再对①单独报错：真忘了
// dry-run 时，受影响的外壳会从产出里整体消失，调用方自己打印的
// "已产出（N 个外壳）"里 N 会明显偏小，这是调用方（cmd/be-ops）该负责
// 的可见性，不是本包该报的错。
func TestGen_缺少local_debug数据时整个外壳静默跳过(t *testing.T) {
	specs := []genyaml.ComponentSpec{mod("mdm/customer", "go-shell-core", nil)}
	out, err := Gen(specs, map[string]map[string]string{})
	if err != nil {
		t.Fatalf("不应该报错，应该静默跳过：%v", err)
	}
	if len(out) != 0 {
		t.Fatalf("数据缺失的外壳应该完全不出现在产出里，实际 %+v", out)
	}
}

// TestGen_外壳还没原子式切换完时跳过不报错 是阶段四 Task 6 真机跑到的
// 场景：registry/schemas.tsv 从阶段一就把全部 4 个外壳分好组了，但各
// 外壳原子式切换成 local: true 是分任务做的（Task 6 先切 3 个 Go 外壳，
// Task 7 才轮到 py-render）。py-render 只有 infra/print 一个成员、还没
// 切换时，不该拖累其余三个已经切换完的 Go 外壳——那是"这个外壳还没
// 轮到"，判据是"这个外壳的成员是否都能在 localDebug 里查到"，不是
// `ComponentSpec.Local`（那个字段反映 assembly.yaml 的既定意图，从
// 阶段一起对全部 4 个外壳就一直是 true，没有区分力）。
func TestGen_外壳还没原子式切换完时跳过不报错(t *testing.T) {
	notYetMerged := genyaml.ComponentSpec{ID: "infra/print", Version: "1.0.0", Shell: "py-render", Local: true}
	specs := []genyaml.ComponentSpec{
		mod("mdm/customer", "go-shell-core", nil),
		notYetMerged,
	}
	localDebug := map[string]map[string]string{
		"mdm/customer": {"COMPONENT_ID": "mdm/customer"},
		// 故意不给 infra/print 任何 local-debug 数据——它 brickkit.yaml
		// 里还没真的写 local: true，brickkit up --dry-run 根本不会为它
		// 生成这份文件，这是正常状态。
	}

	out, err := Gen(specs, localDebug)
	if err != nil {
		t.Fatalf("已切换的外壳不该被还没切换的外壳拖累报错：%v", err)
	}
	if moduleEnv(out, "go-shell-core", "mdm/customer") == nil {
		t.Fatal("go-shell-core 已经切换完，应该正常出现在结果里")
	}
	for _, sh := range out {
		if sh.Name == "py-render" {
			t.Fatal("py-render 还没原子式切换完，不应该出现在产出里")
		}
	}
}

func moduleEnv(shells []ShellEnv, shellName, componentID string) map[string]string {
	for _, sh := range shells {
		if sh.Name != shellName {
			continue
		}
		for _, m := range sh.Modules {
			if m.ComponentID == componentID {
				return m.Env
			}
		}
	}
	return nil
}
