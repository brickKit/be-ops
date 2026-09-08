package authzreg

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

var permissionsHeader = []string{"key", "title", "type", "owner_component", "deprecated"}
var dataScopesHeader = []string{"component", "dimension", "column", "mode", "tables"}

// ReadPermissionsTSV 读 registry/permissions.tsv 的既有内容——这是"只增
// 不改"的历史基线，GenPermissions 拿它来跟本次扫描合并。文件只有表头
// （尚无数据行）时返回空切片，不是错误。
func ReadPermissionsTSV(path string) ([]PermissionRow, error) {
	lines, err := readDataLines(path)
	if err != nil {
		return nil, err
	}
	rows := make([]PermissionRow, 0, len(lines))
	for _, l := range lines {
		cols := strings.Split(l, "\t")
		if len(cols) != 5 {
			return nil, fmt.Errorf("%s 有一行不是 5 列：%q", path, l)
		}
		rows = append(rows, PermissionRow{
			Key: cols[0], Title: cols[1], Type: cols[2], OwnerComponent: cols[3], Deprecated: cols[4],
		})
	}
	return rows, nil
}

// WritePermissionsTSV 落盘，表头固定不变。
func WritePermissionsTSV(path string, rows []PermissionRow) error {
	var b strings.Builder
	b.WriteString(strings.Join(permissionsHeader, "\t") + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", r.Key, r.Title, r.Type, r.OwnerComponent, r.Deprecated)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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

// readDataLines 跳过表头与空行——两张表都是"第一行表头 + 数据行"的形状。
func readDataLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}
