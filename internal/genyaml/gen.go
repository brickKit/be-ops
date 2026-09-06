// Package genyaml 读各组件的 component.yaml + assembly.yaml，产出
// brickkit.yaml（总纲 §2.4 产出 5，随带产出 2b 的 bindings）。
package genyaml

import (
	"fmt"
	"regexp"
	"strings"
)

// Dependency 是 component.yaml 的 dependencies.components 一条。
type Dependency struct {
	ID       string
	Optional bool
}

// ComponentSpec 是 component.yaml + assembly.yaml 合并后的视图——genyaml
// 只关心生成 brickkit.yaml 需要的那些字段，不是两份 yaml 的完整镜像。
type ComponentSpec struct {
	ID           string
	Version      string
	Labels       map[string]any // deployment.labels 原始值（可能是 bool/int/string）
	Dependencies []Dependency

	// assembly.yaml 的 asset.assembly_role：default/optional/slot/
	// channel:<name>/reserve/blueprint
	AssemblyRole string
	SlotName     string // 仅 AssemblyRole == "slot" 时有意义

	Shell string // 合并部署时进哪个外壳（决定 bindings 挂在哪条资源条目下）

	NeedsDatabase bool // 声明了 { kind: database } 资源依赖

	// 阶段四合并部署才会用到，阶段一测试用它验证 local 注释规则
	Local     bool
	LocalPort int
}

var exactVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// aggregatorIDs 是聚合型组件——它们要调各业务组件的 batchGet，依赖清单会
// 长到几十条，写成强依赖的话客户没买那个组件时整个聚合组件就起不来
// （总纲 §2.4 生成器铁律三）。
var aggregatorIDs = map[string]bool{
	"infra/bff-mobile":   true,
	"infra/notification": true,
}

// Gen 产出 brickkit.yaml。specs 是全量组件视图，enabledIDs 是客户买了的
// 那些——不在 enabledIDs 里的组件**整条不出现**，不是 enabled: false
// （后者会级联关掉下游主数据，总纲 §2.4 生成器铁律二）。
func Gen(specs []ComponentSpec, enabledIDs []string) (string, error) {
	enabled := make(map[string]bool, len(enabledIDs))
	for _, id := range enabledIDs {
		enabled[id] = true
	}

	var selected []ComponentSpec
	for _, s := range specs {
		if enabled[s.ID] {
			selected = append(selected, s)
		}
	}

	if err := validate(selected); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("components:\n")
	for _, s := range selected {
		writeComponent(&b, s)
	}

	if hasDatabaseDep(selected) {
		b.WriteString("\nresources:\n")
		writeDatabaseBindings(&b, selected)
	}

	return b.String(), nil
}

func validate(selected []ComponentSpec) error {
	slotOwners := map[string]string{} // slot_name -> 第一个占用它的组件 ID
	for _, s := range selected {
		if !exactVersionRe.MatchString(s.Version) {
			return fmt.Errorf("%s 的 version %q 不是精确版本（不许 ^ / ~ / latest，决策 108）", s.ID, s.Version)
		}
		if aggregatorIDs[s.ID] {
			for _, d := range s.Dependencies {
				if !d.Optional {
					return fmt.Errorf("聚合型组件 %s 的依赖 %s 必须是 optional（总纲 §2.4 生成器铁律三）", s.ID, d.ID)
				}
			}
		}
		if s.AssemblyRole == "slot" {
			if owner, ok := slotOwners[s.SlotName]; ok {
				return fmt.Errorf("slot_name %q 同时被 %s 与 %s 占用，同一环境只能装一个（§5.11）", s.SlotName, owner, s.ID)
			}
			slotOwners[s.SlotName] = s.ID
		}
	}
	return nil
}

func writeComponent(b *strings.Builder, s ComponentSpec) {
	fmt.Fprintf(b, "  - id: %s\n", s.ID)
	fmt.Fprintf(b, "    version: %s\n", s.Version)
	if s.Local {
		// ⚠️ local: true 的语义是"这个组件跑在开发者的 IDE 里"——交付现场
		// 写着一串 local: true 时，读的人会以为有人正在调试。必须在同一
		// 个文件里写清楚不是（设计书 §13.1）。
		b.WriteString("    # ⚠️ local: true 不是有人在调试，是合并部署（be-ops 生成，设计书 §13.1）\n")
		b.WriteString("    local: true\n")
		fmt.Fprintf(b, "    localPort: %d\n", s.LocalPort)
	}
	for _, d := range s.Dependencies {
		if d.Optional {
			fmt.Fprintf(b, "    # 依赖 %s（optional）\n", d.ID)
		} else {
			fmt.Fprintf(b, "    # 依赖 %s（强依赖）\n", d.ID)
		}
	}
	if len(s.Labels) > 0 {
		b.WriteString("    labels:\n")
		for k, v := range s.Labels {
			// ⚠️ Docker labels / K8s annotations 的值必须是字符串——平台
			// 不做自动转换（总纲 §2.4 生成器铁律一）。
			fmt.Fprintf(b, "      %s: %q\n", k, fmt.Sprint(v))
		}
	}
}

func hasDatabaseDep(selected []ComponentSpec) bool {
	for _, s := range selected {
		if s.NeedsDatabase {
			return true
		}
	}
	return false
}

// writeDatabaseBindings 把每个声明了数据库资源的组件挂上 componentId
// bindings（总纲产出 2b）。阶段一（未合并）时每个组件按自己的 Shell
// 分组挂在对应的资源条目下——即使还没有真的合并进一个进程，用同一套
// "共享外壳登录角色 + SET LOCAL ROLE" 的凭据结构，阶段四合并时数据库
// 侧不用再迁移一次。
func writeDatabaseBindings(b *strings.Builder, selected []ComponentSpec) {
	byShell := map[string][]string{}
	var shellOrder []string
	for _, s := range selected {
		if !s.NeedsDatabase {
			continue
		}
		if _, ok := byShell[s.Shell]; !ok {
			shellOrder = append(shellOrder, s.Shell)
		}
		byShell[s.Shell] = append(byShell[s.Shell], s.ID)
	}
	for _, shell := range shellOrder {
		fmt.Fprintf(b, "  - kind: database\n")
		fmt.Fprintf(b, "    engine: postgresql\n")
		fmt.Fprintf(b, "    bindings:\n")
		for _, id := range byShell[shell] {
			fmt.Fprintf(b, "      - componentId: %s\n", id)
			fmt.Fprintf(b, "        database: brickkit_db\n")
		}
	}
}
