// Package authzreg 从各组件 assembly.yaml 的 permissions/data_scopes 段
// 聚合出 registry/permissions.tsv（产出 9）与 registry/data-scopes.tsv
// （产出 10，第 14 章）。判据：
//   - permissions.tsv 只增不改（导读四张钉死的表之一）——已发布的 key
//     永远保留，重跑本命令绝不删除旧行，只更新非 key 列。
//   - data_scopes 段省略即报错（导读第 22 条：安全机制的默认值只能
//     fail-closed）——不需要必须显式写 data_scopes: none，省略 ≠ none。
package authzreg

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PermissionDecl 是 assembly.yaml permissions 段的一条。
type PermissionDecl struct {
	Key   string `yaml:"key"`
	Title string `yaml:"title"`
	Type  string `yaml:"type"`
}

// DataScopeDecl 是 assembly.yaml data_scopes 段（非 none 时）的一条。
type DataScopeDecl struct {
	Dimension string   `yaml:"dimension"`
	Column    string   `yaml:"column"`
	Mode      string   `yaml:"mode"`
	Tables    []string `yaml:"tables"`
}

// AssemblyDecl 是一个组件 assembly.yaml 里与权限体系相关的声明。
type AssemblyDecl struct {
	ComponentID string
	Permissions []PermissionDecl
	// DataScopesNone 为 true 表示该组件显式声明 data_scopes: none。
	DataScopesNone bool
	DataScopes     []DataScopeDecl
}

// dataScopesField 区分三种情况：完全省略（*dataScopesField 为 nil）、
// 标量 "none"、序列。省略即报错是导读第 22 条的判据，必须在这里能被
// 上层分辨出来——所以 assemblyYAML.DataScopes 的类型是
// *dataScopesField 而不是值类型：yaml.v3 对文档里不存在的字段保留
// 指针的零值 nil，只有字段真的出现过才会分配并调用 UnmarshalYAML。
type dataScopesField struct {
	isNone  bool
	entries []DataScopeDecl
}

func (d *dataScopesField) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value != "none" {
			return fmt.Errorf("data_scopes 的标量值只能是 none，实际是 %q", node.Value)
		}
		d.isNone = true
		return nil
	case yaml.SequenceNode:
		return node.Decode(&d.entries)
	default:
		return fmt.Errorf("data_scopes 必须是 none 或一个维度列表")
	}
}

type assemblyYAML struct {
	ID          string           `yaml:"id"`
	Permissions []PermissionDecl `yaml:"permissions"`
	DataScopes  *dataScopesField `yaml:"data_scopes"`
}

// LoadAssemblyDecls 扫描 root（通常是 components/）下所有
// <domain>/<name>/assembly.yaml，聚合权限键与数据权限声明。目录结构是
// <domain>/<name>/，匹配 components/ 的实际布局（同 genyaml.Load）。
//
// ⚠️ 省略 data_scopes 段当场报错（导读第 22 条）——这条只对"确实有
// assembly.yaml 的目录"生效，目录里没有 assembly.yaml（组件还没写完）
// 不在本函数管辖范围，直接跳过。
func LoadAssemblyDecls(root string) ([]AssemblyDecl, error) {
	var decls []AssemblyDecl

	domains, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		domainPath := filepath.Join(root, domain.Name())
		names, err := os.ReadDir(domainPath)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			dir := filepath.Join(domainPath, name.Name())
			asmPath := filepath.Join(dir, "assembly.yaml")
			if _, err := os.Stat(asmPath); os.IsNotExist(err) {
				continue
			}
			decl, err := loadOne(asmPath)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", asmPath, err)
			}
			decls = append(decls, decl)
		}
	}
	return decls, nil
}

func loadOne(asmPath string) (AssemblyDecl, error) {
	data, err := os.ReadFile(asmPath)
	if err != nil {
		return AssemblyDecl{}, err
	}
	var asm assemblyYAML
	if err := yaml.Unmarshal(data, &asm); err != nil {
		return AssemblyDecl{}, err
	}
	if asm.DataScopes == nil {
		return AssemblyDecl{}, fmt.Errorf(
			"省略了 data_scopes 段——不需要也要显式写 data_scopes: none（导读第 22 条，设计书 §14.2.2）")
	}
	return AssemblyDecl{
		ComponentID:    asm.ID,
		Permissions:    asm.Permissions,
		DataScopesNone: asm.DataScopes.isNone,
		DataScopes:     asm.DataScopes.entries,
	}, nil
}
