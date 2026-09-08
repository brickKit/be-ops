package authzreg

import (
	"fmt"
	"sort"
	"strings"
)

// PermissionRow 对应 registry/permissions.tsv 的一行。
type PermissionRow struct {
	Key            string
	Title          string
	Type           string
	OwnerComponent string
	Deprecated     string
}

// DataScopeRow 对应 registry/data-scopes.tsv 的一行。
type DataScopeRow struct {
	Component string
	Dimension string
	Column    string
	Mode      string
	Tables    string // 逗号分隔，原样落盘
}

// GenPermissions 把已有表（只增不改的历史基线）与本次扫描到的声明合并。
//
// 判据（导读四张钉死的表）：
//   - 已存在的 key 永远保留，即使这次扫描里已经没有任何组件声明它（比如
//     组件被临时移出 components/、或 key 被从 assembly.yaml 删掉）——
//     废弃走 deprecated 墓碑列，be-ops 自己绝不悄悄删行。这种"孤儿 key"
//     通过 warnings 报出来，交给人决定要不要手工打 deprecated。
//   - 已存在的 key 如果这次扫描仍然声明，允许刷新 title/type/
//     owner_component——这几列只是描述性信息，"只增不改"保护的是 key
//     这个跨版本持久标识本身，不是它的展示文本；deprecated 列本函数
//     从不设置也不清空，那是人工决定。
//   - 同一个 key 被两个不同组件声明，直接报错——这是命名冲突，不是可以
//     静默择一的情况。
func GenPermissions(existing []PermissionRow, decls []AssemblyDecl) ([]PermissionRow, []string, error) {
	byKey := make(map[string]PermissionRow, len(existing))
	for _, r := range existing {
		byKey[r.Key] = r
	}
	declaredThisRun := make(map[string]bool)

	for _, d := range decls {
		for _, p := range d.Permissions {
			if prev, ok := byKey[p.Key]; ok && prev.OwnerComponent != "" && prev.OwnerComponent != d.ComponentID {
				return nil, nil, fmt.Errorf(
					"权限键 %s 被 %s 与 %s 同时声明——权限键必须全局唯一", p.Key, prev.OwnerComponent, d.ComponentID)
			}
			deprecated := byKey[p.Key].Deprecated
			byKey[p.Key] = PermissionRow{
				Key: p.Key, Title: p.Title, Type: p.Type,
				OwnerComponent: d.ComponentID, Deprecated: deprecated,
			}
			declaredThisRun[p.Key] = true
		}
	}

	var warnings []string
	for k, r := range byKey {
		if !declaredThisRun[k] && r.Deprecated == "" {
			warnings = append(warnings, fmt.Sprintf(
				"权限键 %s（owner=%s）这次扫描没有被任何组件声明，但也没有标 deprecated——"+
					"如果确实不再需要，请手工在 permissions.tsv 打 deprecated 墓碑，不要直接删行",
				k, r.OwnerComponent))
		}
	}
	sort.Strings(warnings)

	rows := make([]PermissionRow, 0, len(byKey))
	for _, r := range byKey {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, warnings, nil
}

// GenDataScopes 是纯派生（registry/README.md：data-scopes.tsv 不需要
// 防改），每次全量重生成，不接受历史基线。
func GenDataScopes(decls []AssemblyDecl) []DataScopeRow {
	var rows []DataScopeRow
	for _, d := range decls {
		if d.DataScopesNone {
			continue
		}
		for _, s := range d.DataScopes {
			rows = append(rows, DataScopeRow{
				Component: d.ComponentID, Dimension: s.Dimension,
				Column: s.Column, Mode: s.Mode, Tables: strings.Join(s.Tables, ","),
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Component != rows[j].Component {
			return rows[i].Component < rows[j].Component
		}
		return rows[i].Dimension < rows[j].Dimension
	})
	return rows
}
