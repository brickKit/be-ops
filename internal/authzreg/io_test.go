package authzreg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPermissionsTSV_只有表头返回空切片(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "permissions.tsv")
	write(t, path, "key\ttitle\ttype\towner_component\tdeprecated\n")

	rows, err := ReadPermissionsTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("只有表头应该返回空切片，得到 %+v", rows)
	}
}

func TestReadPermissionsTSV_读回写入的内容(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "permissions.tsv")

	want := []PermissionRow{
		{Key: "erp.sales.view", Title: "查看销售订单", Type: "page", OwnerComponent: "erp/sales", Deprecated: ""},
		{Key: "mdm.customer.old", Title: "旧键", Type: "action", OwnerComponent: "mdm/customer", Deprecated: "2026-01-01"},
	}
	if err := WritePermissionsTSV(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPermissionsTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("行数不对：期望 %d 得到 %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 行往返不一致：写入 %+v，读回 %+v", i, want[i], got[i])
		}
	}
}

func TestWriteDataScopesTSV_表头固定(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data-scopes.tsv")
	if err := WriteDataScopesTSV(path, []DataScopeRow{
		{Component: "erp/sales", Dimension: "org", Column: "dept_path", Mode: "prefix", Tables: "sales_orders"},
	}); err != nil {
		t.Fatal(err)
	}
	data := readFile(t, path)
	wantHeader := "component\tdimension\tcolumn\tmode\ttables\n"
	if data[:len(wantHeader)] != wantHeader {
		t.Fatalf("表头不对：%q", data[:len(wantHeader)])
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
