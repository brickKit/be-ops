package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

const permV1 = "key\ttitle\ttype\towner_component\tdeprecated\nerp.sales.view\t查看订单\tpage\terp/sales\t\n"

func permProject(t *testing.T) string {
	root := t.TempDir()
	testfixture.Write(t, root, "components/erp/sales/assembly.yaml", "id: erp/sales\ndata_scopes: none\npermissions:\n"+
		"  - { key: erp.sales.view, title: 查看订单, type: page }\n"+
		"  - { key: erp.sales.share, title: 共享订单, type: action, delegable: false }\n")
	testfixture.Write(t, root, "registry/permissions.tsv", permV1)
	testfixture.Write(t, root, "base.tsv", permV1)
	return root
}

func TestPermissions_CheckFailsWhenStaleAndGenerationKeepsOldRows(t *testing.T) {
	root := permProject(t)
	if err := runPermissions([]string{"--check", "--root", root}); err == nil {
		t.Fatal("a missing key must fail --check")
	}
	if got := testfixture.Read(t, root, "registry/permissions.tsv"); got != permV1 {
		t.Fatalf("--check wrote the file:\n%s", got)
	}
	if err := runPermissions([]string{"--root", root, "--base", filepath.Join(root, "base.tsv")}); err != nil {
		t.Fatalf("generation against the committed base: %v", err)
	}
	got := testfixture.Read(t, root, "registry/permissions.tsv")
	if !strings.Contains(got, "erp.sales.share\t共享订单\taction\terp/sales\t\tfalse\n") ||
		!strings.Contains(got, "erp.sales.view\t查看订单\tpage\terp/sales\t\n") {
		t.Fatalf("file:\n%s", got)
	}
	if err := runPermissions([]string{"--check", "--root", root, "--base", filepath.Join(root, "base.tsv")}); err != nil {
		t.Fatalf("now current and append-only: %v", err)
	}
}

func TestPermissions_CheckRefusesARemovedRowAgainstTheBase(t *testing.T) {
	root := permProject(t)
	testfixture.Write(t, root, "base.tsv", permV1+"erp.sales.old\t旧\taction\terp/sales\t\n")
	_ = runPermissions([]string{"--root", root})
	if err := runPermissions([]string{"--check", "--root", root, "--base", filepath.Join(root, "base.tsv")}); err == nil {
		t.Fatal("a key present in the base and gone now must fail")
	}
}
