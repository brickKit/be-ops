// Package shellconfig 产出 4（合并清单）——总纲 §2.4："哪些组件进哪个
// 外壳、端口分配、迁移执行顺序"，生成对象是 `be-shell-go`/`be-shell-python`
// 实际读取的格式（`shell.ModuleSpec`/`ModuleSpec` 的数据来源，见阶段四
// 计划 Task 2/3）。
//
// ⚠️ 端口分配不是本包分配的——`genyaml.Load` 已经把 `deployment.port`
// 读进 `ComponentSpec.Port`（Task 4 顺带给 genyaml 补的那一步），本包
// 只是原样转发，不发明新的端口来源（设计书 §13.8.1）。
package shellconfig

import (
	"fmt"
	"sort"

	"github.com/brickKit/be-ops/internal/genyaml"
)

// Module 是一个外壳里的一个模块——`be-shell-go`/`be-shell-python` 的
// `ModuleSpec` 从这份数据构造，字段含义逐一对应。
type Module struct {
	ComponentID string         `json:"componentId"`
	Version     string         `json:"version"`
	Schema      string         `json:"schema"`
	HTTPPort    int            `json:"httpPort"`
	ExtraPorts  map[string]int `json:"extraPorts,omitempty"`
}

// Shell 是一个外壳实例——本阶段对应 `go-shell-core`/`go-shell-backoffice`/
// `go-shell-infra`/`py-shell-render` 四个真实部署的容器实例（`shell` 字段
// 相同的组件属于同一个外壳，不是同一份镜像/仓库=同一个外壳——三个 Go
// 外壳实例共用同一个 `be-shell-go` 镜像，见阶段四计划 Task 5/6）。
type Shell struct {
	Name    string   `json:"name"`
	Modules []Module `json:"modules"`
}

// Gen 按 `Shell` 字段分组，组内按依赖关系做拓扑排序——**只看同一个外壳
// 内部的依赖边**，跨外壳依赖不影响这个外壳自己的启动/迁移顺序（跨外壳
// 依赖归产出 7/8 处理，产出 4 只管一个外壳内部"谁先谁后"）。没有
// `Shell` 字段的组件（本阶段不合并的 TS/前端/还没轮到的组件）不出现在
// 任何 Shell 里。
func Gen(specs []genyaml.ComponentSpec) ([]Shell, error) {
	grouped := map[string][]genyaml.ComponentSpec{}
	var shellOrder []string
	for _, s := range specs {
		if s.Shell == "" {
			continue
		}
		if _, ok := grouped[s.Shell]; !ok {
			shellOrder = append(shellOrder, s.Shell)
		}
		grouped[s.Shell] = append(grouped[s.Shell], s)
	}
	sort.Strings(shellOrder)

	shells := make([]Shell, 0, len(shellOrder))
	for _, name := range shellOrder {
		ordered, err := topoSort(grouped[name])
		if err != nil {
			return nil, fmt.Errorf("外壳 %s: %w", name, err)
		}
		modules := make([]Module, 0, len(ordered))
		for _, s := range ordered {
			modules = append(modules, Module{
				ComponentID: s.ID,
				Version:     s.Version,
				Schema:      s.Schema,
				HTTPPort:    s.Port,
				ExtraPorts:  s.ExtraPorts,
			})
		}
		shells = append(shells, Shell{Name: name, Modules: modules})
	}
	return shells, nil
}

// topoSort 对同一个外壳内部的组件按依赖关系排序（依赖方排在被依赖方
// 之后）——迁移与启动都要按这个顺序：被依赖的模块（通常是零依赖的
// mdm 类只读枢纽）先迁移、先起，依赖它们的模块后迁移、后起。
//
// 只统计"被依赖方也在同一个外壳里"的边——跨外壳依赖不影响这个外壳
// 内部的顺序（那条边由产出 7/8 处理）。用 Kahn 算法，同一层内按
// ComponentID 排序保证输出确定性，不会因为 map 遍历顺序不同而每次
// 生成的顺序都不一样。
func topoSort(specs []genyaml.ComponentSpec) ([]genyaml.ComponentSpec, error) {
	inShell := make(map[string]bool, len(specs))
	for _, s := range specs {
		inShell[s.ID] = true
	}

	indegree := make(map[string]int, len(specs))
	dependents := make(map[string][]string) // id -> 依赖它的那些 id（同外壳内）
	byID := make(map[string]genyaml.ComponentSpec, len(specs))
	for _, s := range specs {
		byID[s.ID] = s
		indegree[s.ID] = 0
	}
	for _, s := range specs {
		for _, d := range s.Dependencies {
			if !inShell[d.ID] {
				continue // 跨外壳依赖，不参与这个外壳内部的排序
			}
			dependents[d.ID] = append(dependents[d.ID], s.ID)
			indegree[s.ID]++
		}
	}

	var queue []string
	for id, n := range indegree {
		if n == 0 {
			queue = append(queue, id)
		}
	}

	var ordered []genyaml.ComponentSpec
	for len(queue) > 0 {
		sort.Strings(queue)
		id := queue[0]
		queue = queue[1:]
		ordered = append(ordered, byID[id])

		next := append([]string(nil), dependents[id]...)
		sort.Strings(next)
		for _, dep := range next {
			indegree[dep]--
			if indegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(ordered) != len(specs) {
		return nil, fmt.Errorf("同一个外壳内部的依赖图有环，无法排出迁移/启动顺序（%d/%d 个组件排出）", len(ordered), len(specs))
	}
	return ordered, nil
}
