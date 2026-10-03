package authzreg

import (
	"fmt"
	"os"
	"strings"
)

// permissionsColumns are the columns of registry/permissions.tsv in order. Columns are only ever
// appended (never inserted, renamed or removed), so an older file's header is a prefix of this
// list. The first permissionsV1Width columns are always written; a column added later is written
// only up to a row's last non-empty one, so adding a column rewrites the header and leaves every
// existing row byte for byte as it was.
var permissionsColumns = []string{"key", "title", "type", "owner_component", "deprecated", "delegable"}

const permissionsV1Width = 5

var dataScopesHeader = []string{"component", "dimension", "column", "mode", "tables"}

// ReadPermissionsTSV 读 registry/permissions.tsv 的既有内容——这是"只增
// 不改"的历史基线，GenPermissions 拿它来跟本次扫描合并。文件只有表头
// （尚无数据行）时返回空切片，不是错误。列按表头的名字找：表头必须是
// permissionsColumns 的前缀；一行比表头短时缺的尾列为空（五列的旧文件读出
// delegable 为空 = 未声明）。
func ReadPermissionsTSV(path string) ([]PermissionRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePermissions(path, raw)
}

func parsePermissions(name string, raw []byte) ([]PermissionRow, error) {
	header, lines := splitTable(raw)
	if len(header) == 0 && len(lines) == 0 {
		return []PermissionRow{}, nil
	}
	if err := checkHeader(name, header); err != nil {
		return nil, err
	}
	rows := make([]PermissionRow, 0, len(lines))
	for _, l := range lines {
		cols := strings.Split(l, "\t")
		if len(cols) > len(header) {
			return nil, fmt.Errorf("%s 有一行比表头（%d 列）宽：%q", name, len(header), l)
		}
		cols = append(cols, make([]string, len(permissionsColumns)-len(cols))...)
		if cols[0] == "" {
			return nil, fmt.Errorf("%s 有一行没有 key：%q", name, l)
		}
		rows = append(rows, PermissionRow{Key: cols[0], Title: cols[1], Type: cols[2], OwnerComponent: cols[3],
			Deprecated: cols[4], Delegable: cols[5]})
	}
	return rows, nil
}

func checkHeader(name string, header []string) error {
	if len(header) < permissionsV1Width || len(header) > len(permissionsColumns) {
		return fmt.Errorf("%s 的表头 %q 不是 %q 的前缀", name, header, permissionsColumns)
	}
	for i, h := range header {
		if h != permissionsColumns[i] {
			return fmt.Errorf("%s 的表头第 %d 列是 %q，应为 %q（列只在末尾追加）", name, i+1, h, permissionsColumns[i])
		}
	}
	return nil
}

// WritePermissionsTSV 落盘：表头是全部列，每行至少写前五列，后加的列写到该行最后一个非空列为止。
func WritePermissionsTSV(path string, rows []PermissionRow) error {
	return os.WriteFile(path, RenderPermissionsTSV(rows), 0o644)
}

// RenderPermissionsTSV renders the file WritePermissionsTSV writes.
func RenderPermissionsTSV(rows []PermissionRow) []byte {
	var b strings.Builder
	b.WriteString(strings.Join(permissionsColumns, "\t") + "\n")
	for _, r := range rows {
		cols := []string{r.Key, r.Title, r.Type, r.OwnerComponent, r.Deprecated, r.Delegable}
		n := len(cols)
		for n > permissionsV1Width && cols[n-1] == "" {
			n--
		}
		b.WriteString(strings.Join(cols[:n], "\t") + "\n")
	}
	return []byte(b.String())
}

// splitTable splits a TSV file into its header and its non-empty data lines.
func splitTable(raw []byte) ([]string, []string) {
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil, nil
	}
	var data []string
	for _, l := range lines[1:] {
		if l != "" {
			data = append(data, l)
		}
	}
	return strings.Split(lines[0], "\t"), data
}

// WriteDataScopesTSV 落盘——纯派生表，不需要先读旧内容合并。
func WriteDataScopesTSV(path string, rows []DataScopeRow) error {
	var b strings.Builder
	b.WriteString(strings.Join(dataScopesHeader, "\t") + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", r.Component, r.Dimension, r.Column, r.Mode, r.Tables)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
