// Package shellenv 产出 7（每外壳环境变量表）——总纲 §2.4，设计书
// §13.8.2 的规则：同外壳依赖用 `http://127.0.0.1:<对方端口>`，跨外壳/
// 独立容器依赖用 `http://<宿主机地址>:<对方端口>`。
//
// ⚠️ 本包只做"重写"这一步，不负责生成 local-debug 的原始内容——那份
// 数据来自真实的 `brickkit up --dry-run`（`cmd/be-ops` 负责读文件、
// 本包只处理已经解析成 map 的 env）。设计书 §13.8.2 原话："自己另算
// 的那份，早晚和平台的算法分叉"——本包因此只重写"依赖地址"这一类 key
// （识别方式：值形如 `http://localhost:<port>`，那正是 brickKit 对
// `local:true` 依赖的既有重写结果，见 §13.1），其余 key 原样透传。
//
// ⚠️ 外壳分组（`registry/schemas.tsv`）与外壳原子式切换（`local: true`）
// 不是同一时间发生的——前者阶段一就写死了全部 4 个外壳，后者是分阶段
// 任务做的（阶段四 Task 6 先切 3 个 Go 外壳，Task 7 才轮到 py-render）。
// `Gen` 因此按外壳为单位做门槛：一个外壳只要还有成员没 `local: true`，
// 整个外壳直接跳过、不报错——那是"还没轮到"，不是"忘了先
// `brickkit up --dry-run`"（真机撞到过这个场景，见阶段四 Task 6）。
package shellenv

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/brickKit/be-ops/internal/genyaml"
)

// HostGatewayAddr 是"跨外壳/独立容器需要从容器内部访问宿主机"的既有
// 约定字面量——`brickkit.yaml` 的 `resources[].host` 已经在用同一个值
// （设计书 §13.8.3，brickKit `006` §10.4 的建议），这里复用，不是发明
// 一个新地址。真机部署需要外壳的 compose 服务显式声明
// `extra_hosts: ["host.docker.internal:host-gateway"]` 才能在 Linux 上
// 解析成功（Docker Desktop 原生支持，不需要这一行）——这是产出 8 生成
// shell-compose.yml 时要带上的一条，不是本包的职责。
const HostGatewayAddr = "host.docker.internal"

// ModuleEnv 是外壳里一个模块最终应该拿到的 env map（已经完成依赖地址
// 重写）。
type ModuleEnv struct {
	ComponentID string
	Env         map[string]string
}

// ShellEnv 是一个外壳实例的全部模块 env。
type ShellEnv struct {
	Name    string
	Modules []ModuleEnv
}

// Gen 按每个模块自己的 local-debug env（`localDebug` 以 componentID 为
// key）重写依赖地址：依赖方也在合并部署里（`Shell != ""`）时，
// brickKit 生成的 local-debug 原始值统一是 `http://localhost:<对方
// localPort>`——同外壳时这个值本来就对（同一个进程同一个 localhost），
// 原样保留；跨外壳时把 host 换成 HostGatewayAddr。依赖方本来就没有
// 合并部署（`Shell == ""`，含真实独立容器）时，local-debug 给的已经是
// 正常的容器 DNS 地址，原样透传，不触碰。
func Gen(specs []genyaml.ComponentSpec, localDebug map[string]map[string]string) ([]ShellEnv, error) {
	byID := make(map[string]genyaml.ComponentSpec, len(specs))
	for _, s := range specs {
		byID[s.ID] = s
	}

	grouped := map[string][]genyaml.ComponentSpec{}
	var shellOrder []string
	seen := map[string]bool{}
	for _, s := range specs {
		if s.Shell == "" {
			continue
		}
		if !seen[s.Shell] {
			seen[s.Shell] = true
			shellOrder = append(shellOrder, s.Shell)
		}
		grouped[s.Shell] = append(grouped[s.Shell], s)
	}

	shells := make([]ShellEnv, 0, len(shellOrder))
	for _, name := range shellOrder {
		members := grouped[name]

		// ⚠️ 阶段四 Task 6 真机跑到的场景：assembly.yaml 的 shell 字段
		// 早在阶段一就给全部 4 个外壳分好组了，但各外壳原子式切换成
		// local: true 是分任务做的（Task 6 先切 3 个 Go 外壳，Task 7
		// 才轮到 py-render）。`genyaml.ComponentSpec.Local` 反映的是
		// assembly.yaml 的"迟早要合并"这个既定意图，不是 brickkit.yaml
		// 此刻是否真的写了 local: true——两者不是一回事（真机验证时才
		// 发现：Local 对这四个外壳的全部成员从阶段一起就一直是 true，
		// 拿它当"有没有真的切换过"的判据完全没有区分力）。唯一真实反映
		// "brickkit.yaml 此刻是不是已经切了"的信号，是 `brickkit up
		// --dry-run` 到底有没有为这个组件生成 local-debug 文件——所以
		// 判据改成"这个外壳的全部成员是不是都能在 localDebug 里查到"，
		// 缺一个就说明这个外壳整体还没轮到，跳过、不报错。
		allPresent := true
		for _, s := range members {
			if _, ok := localDebug[s.ID]; !ok {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}

		var modules []ModuleEnv
		for _, s := range members {
			raw, ok := localDebug[s.ID]
			if !ok {
				return nil, fmt.Errorf("模块 %s 没有对应的 local-debug env（是不是忘了先 brickkit up --dry-run）", s.ID)
			}
			rewritten, err := rewriteOne(s, raw, byID)
			if err != nil {
				return nil, fmt.Errorf("模块 %s: %w", s.ID, err)
			}
			modules = append(modules, ModuleEnv{ComponentID: s.ID, Env: rewritten})
		}
		shells = append(shells, ShellEnv{Name: name, Modules: modules})
	}
	return shells, nil
}

func rewriteOne(s genyaml.ComponentSpec, raw map[string]string, byID map[string]genyaml.ComponentSpec) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = v
	}

	for _, d := range s.Dependencies {
		dep, ok := byID[d.ID]
		if !ok {
			continue // 依赖方不在本次装配范围内（弱依赖缺失一类情况），local-debug 里本来就没有这个 key
		}
		rewriteKey(out, envVarName(dep.ID)+"_ENDPOINT", s.Shell, dep)
		for extraName := range dep.ExtraPorts {
			key := envVarName(dep.ID) + "_" + strings.ToUpper(extraName) + "_ENDPOINT"
			rewriteKey(out, key, s.Shell, dep)
		}
	}
	return out, nil
}

// rewriteKey 只动"值形如 http://localhost:<port> 且依赖方确实合并
// 部署"的那些 key——这两个条件都满足才说明这是一条需要按跨/同外壳
// 规则重写的依赖地址，其余情况（依赖方本来就是独立容器、或者这个 key
// 根本不存在）原样跳过。
func rewriteKey(env map[string]string, key, myShell string, dep genyaml.ComponentSpec) {
	val, ok := env[key]
	if !ok || dep.Shell == "" {
		return
	}
	const prefix = "http://localhost:"
	if !strings.HasPrefix(val, prefix) {
		return
	}
	port := strings.TrimPrefix(val, prefix)
	if _, err := strconv.Atoi(port); err != nil {
		return // 值不是"localhost:纯数字端口"这个形状，不是本函数要处理的情况，原样保留更安全
	}
	if dep.Shell == myShell {
		env[key] = "http://127.0.0.1:" + port
	} else {
		env[key] = "http://" + HostGatewayAddr + ":" + port
	}
}

// envVarName 把组件 ID（"mdm/customer"）转成 ENDPOINT 变量名前缀
// （"MDM_CUSTOMER"）——`/` 与 `-` 都换成 `_`，全大写，与总纲 §2.1 的
// 既有算法逐字对应（`ERP_SALES_ENDPOINT`/`ERP_SALES_GRPC_ENDPOINT`
// 的既有例子）。
func envVarName(id string) string {
	replaced := strings.NewReplacer("/", "_", "-", "_").Replace(id)
	return strings.ToUpper(replaced)
}
