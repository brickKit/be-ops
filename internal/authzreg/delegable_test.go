package authzreg

import (
	"path/filepath"
	"strings"
	"testing"
)

func ptr(b bool) *bool { return &b }

// A file written before the delegable column existed: five columns, the header without delegable.
const v1File = "key\ttitle\ttype\towner_component\tdeprecated\n" +
	"erp.sales.view\t查看订单\tpage\terp/sales\t\n" +
	"erp.sales.old\t旧键\taction\terp/sales\t2026-01-01\n"

func TestReadPermissionsTSV_FiveColumnRowsReadAsDelegableUnstated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.tsv")
	write(t, path, v1File)
	rows, err := ReadPermissionsTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Delegable != "" || rows[1].Deprecated != "2026-01-01" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestReadPermissionsTSV_ColumnsAreFoundByHeaderName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.tsv")
	write(t, path, "key\ttitle\ttype\towner_component\tdeprecated\tdelegable\n"+
		"erp.sales.share\t共享订单\taction\terp/sales\t\tfalse\n")
	rows, err := ReadPermissionsTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Delegable != "false" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestReadPermissionsTSV_RefusesARowWiderThanTheHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.tsv")
	write(t, path, v1File+"erp.sales.x\tx\taction\terp/sales\t\tfalse\n")
	if _, err := ReadPermissionsTSV(path); err == nil {
		t.Fatal("a sixth field without a sixth header column must be refused")
	}
}

// The append-only promise: adding the column rewrites the header, and no existing row.
func TestWritePermissionsTSV_AddingTheColumnLeavesEveryOldRowByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.tsv")
	write(t, path, v1File)
	rows, err := ReadPermissionsTSV(path)
	if err != nil {
		t.Fatal(err)
	}
	rows = append(rows, PermissionRow{Key: "erp.sales.share", Title: "共享订单", Type: "action", OwnerComponent: "erp/sales", Delegable: "false"})
	if err := WritePermissionsTSV(path, rows); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	oldRows := strings.SplitN(v1File, "\n", 2)[1]
	if !strings.HasPrefix(got, "key\ttitle\ttype\towner_component\tdeprecated\tdelegable\n"+oldRows) {
		t.Fatalf("old rows changed or header wrong:\n%q", got)
	}
	if !strings.HasSuffix(got, "erp.sales.share\t共享订单\taction\terp/sales\t\tfalse\n") {
		t.Fatalf("new row = %q", got)
	}
}

func TestGenPermissions_TakesDelegableFromTheDeclaration(t *testing.T) {
	rows, _, err := GenPermissions(nil, []AssemblyDecl{{ComponentID: "erp/sales", Permissions: []PermissionDecl{
		{Key: "erp.sales.share", Title: "共享订单", Type: "action", Delegable: ptr(false)},
		{Key: "erp.sales.view", Title: "查看订单", Type: "page"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Delegable != "false" || rows[1].Delegable != "" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestGenPermissions_FillsAnEmptyCellButNeverChangesAStatedOne(t *testing.T) {
	existing := []PermissionRow{{Key: "erp.sales.share", Title: "共享订单", Type: "action", OwnerComponent: "erp/sales"}}
	decl := func(d *bool) []AssemblyDecl {
		return []AssemblyDecl{{ComponentID: "erp/sales", Permissions: []PermissionDecl{
			{Key: "erp.sales.share", Title: "共享订单", Type: "action", Delegable: d}}}}
	}
	rows, _, err := GenPermissions(existing, decl(ptr(false)))
	if err != nil || rows[0].Delegable != "false" {
		t.Fatalf("an empty cell takes the stated value: %+v %v", rows, err)
	}
	if rows, _, err = GenPermissions(rows, decl(nil)); err != nil || rows[0].Delegable != "false" {
		t.Fatalf("not stating it any more keeps the released value: %+v %v", rows, err)
	}
	if _, _, err = GenPermissions(rows, decl(ptr(true))); err == nil {
		t.Fatal("changing a released delegable value must be refused")
	}
}

func TestAppendOnly(t *testing.T) {
	base := v1File
	cases := []struct {
		name, next string
		bad        bool
	}{
		{"same file", base, false},
		{"column appended and filled on an old row, new row added",
			"key\ttitle\ttype\towner_component\tdeprecated\tdelegable\n" +
				"erp.sales.view\t查看订单\tpage\terp/sales\t\tfalse\n" +
				"erp.sales.old\t旧键\taction\terp/sales\t2026-01-01\n" +
				"erp.sales.new\t新键\taction\terp/sales\t\n", false},
		{"title refreshed", strings.Replace(base, "查看订单", "查看销售订单", 1), false},
		{"deprecated filled", strings.Replace(base, "erp/sales\t\n", "erp/sales\t2026-10-03\n", 1), false},
		{"row removed", "key\ttitle\ttype\towner_component\tdeprecated\nerp.sales.view\t查看订单\tpage\terp/sales\t\n", true},
		{"tombstone cleared", strings.Replace(base, "2026-01-01", "", 1), true},
		{"type changed", strings.Replace(base, "\tpage\t", "\taction\t", 1), true},
		{"owner changed", strings.Replace(base, "erp.sales.view\t查看订单\tpage\terp/sales", "erp.sales.view\t查看订单\tpage\terp/orders", 1), true},
		{"column removed", "key\ttitle\ttype\towner_component\n", true},
		{"column inserted in the middle", strings.Replace(base, "type\towner", "type\tdelegable\towner", 1), true},
	}
	for _, c := range cases {
		probs, err := AppendOnly([]byte(base), []byte(c.next))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (len(probs) > 0) != c.bad {
			t.Errorf("%s: problems = %v, want bad=%v", c.name, probs, c.bad)
		}
	}
}

func TestAppendOnly_AStatedDelegableNeverChanges(t *testing.T) {
	base := "key\ttitle\ttype\towner_component\tdeprecated\tdelegable\nerp.sales.share\t共享\taction\terp/sales\t\tfalse\n"
	probs, err := AppendOnly([]byte(base), []byte(strings.Replace(base, "\tfalse\n", "\n", 1)))
	if err != nil || len(probs) == 0 {
		t.Fatalf("clearing a stated delegable must be a problem: %v %v", probs, err)
	}
}
