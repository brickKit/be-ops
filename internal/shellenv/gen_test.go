package shellenv

import (
	"testing"

	"github.com/brickKit/be-ops/internal/genyaml"
)

func mod(id, shell string, extraPorts map[string]int, deps ...string) genyaml.ComponentSpec {
	s := genyaml.ComponentSpec{ID: id, Version: "1.0.0", Shell: shell, ExtraPorts: extraPorts}
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

// TestGen_缺少local_debug数据时报错不静默 防的是"忘了先 brickkit up
// --dry-run 就跑产出 7，生成一份缺胳膊少腿的环境变量表"。
func TestGen_缺少local_debug数据时报错不静默(t *testing.T) {
	specs := []genyaml.ComponentSpec{mod("mdm/customer", "go-shell-core", nil)}
	if _, err := Gen(specs, map[string]map[string]string{}); err == nil {
		t.Fatal("期望报错，实际没有")
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
