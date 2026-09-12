// Package shelldepends 产出 8（shell-compose 的 depends_on）——总纲
// §2.4："外壳之间的启动顺序"。平台只排它自己生成的那些容器，外壳它
// 一个都不认识，这层顺序完全要靠 `be-ops` 算出来写进
// `shell-compose.yml`（设计书 §13.8.3）。
//
// ⚠️ 只统计**强依赖**：弱依赖的既有语义是"缺了也能忍"（§3.6，缺失时
// 平台连环境变量都不注入），跨外壳时同样不该逼着 compose 排一个"必须
// 等对方健康"的顺序——那会把一条本该容忍缺失的边，错误地升级成一条
// 启动阻塞边。
package shelldepends

import (
	"fmt"
	"sort"

	"github.com/brickKit/be-ops/internal/genyaml"
)

// ShellDeps 是一个外壳实例的启动顺序声明。
type ShellDeps struct {
	Name      string   `json:"name"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Gen 按每个组件的强依赖，推导出"这个组件所在的外壳"依赖"被依赖组件
// 所在的外壳"这条边——同外壳内部的依赖不计入（那是产出 4 的 topoSort
// 管的事，不是外壳之间的顺序）。
func Gen(specs []genyaml.ComponentSpec) ([]ShellDeps, error) {
	shellOf := make(map[string]string, len(specs)) // componentID -> shell
	var shellOrder []string
	seenShell := map[string]bool{}
	for _, s := range specs {
		if s.Shell == "" {
			continue
		}
		shellOf[s.ID] = s.Shell
		if !seenShell[s.Shell] {
			seenShell[s.Shell] = true
			shellOrder = append(shellOrder, s.Shell)
		}
	}
	sort.Strings(shellOrder)

	dependsOn := make(map[string]map[string]bool, len(shellOrder))
	for _, name := range shellOrder {
		dependsOn[name] = map[string]bool{}
	}

	for _, s := range specs {
		myShell := s.Shell
		if myShell == "" {
			continue
		}
		for _, d := range s.Dependencies {
			if d.Optional {
				continue // 弱依赖不产生跨外壳启动顺序，见包注释
			}
			depShell, ok := shellOf[d.ID]
			if !ok || depShell == "" || depShell == myShell {
				continue // 依赖方不合并部署，或者跟自己在同一个外壳
			}
			dependsOn[myShell][depShell] = true
		}
	}

	if err := checkAcyclic(shellOrder, dependsOn); err != nil {
		return nil, err
	}

	out := make([]ShellDeps, 0, len(shellOrder))
	for _, name := range shellOrder {
		var deps []string
		for dep := range dependsOn[name] {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		out = append(out, ShellDeps{Name: name, DependsOn: deps})
	}
	return out, nil
}

// checkAcyclic 防的是"生成器自己算出一个环"——组件级依赖图本身是无环的
// （全系统的既有设计保证），但把组件粗粒度地合并成外壳之后，理论上
// A 外壳的某个组件依赖 B 外壳、B 外壳的另一个组件又依赖 A 外壳，两条
// 边分别成立却在外壳粒度上成环。真出现这种情况，是分组本身有问题
// （两个应该拆开的外壳被误分到了一起），不能让它悄悄生成一份 Docker
// Compose 用不了的循环 depends_on。
func checkAcyclic(order []string, dependsOn map[string]map[string]bool) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(order))
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		color[name] = gray
		var deps []string
		for d := range dependsOn[name] {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			switch color[dep] {
			case white:
				if err := visit(dep, append(path, dep)); err != nil {
					return err
				}
			case gray:
				return fmt.Errorf("外壳之间的依赖出现环：%v -> %s", append(path, dep), dep)
			}
		}
		color[name] = black
		return nil
	}
	for _, name := range order {
		if color[name] == white {
			if err := visit(name, []string{name}); err != nil {
				return err
			}
		}
	}
	return nil
}
