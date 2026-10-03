// Package testfixture copies the be-protocol fixture components (widget, peer) from the pinned
// module into a test's temporary directory, so generators run against the real fixture files
// and may rewrite them. Test-only helper.
package testfixture

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	beprotocol "github.com/brickKit/be-protocol"
)

// Copy copies fixtures/<name> (widget or peer) into a fresh directory and returns its path.
func Copy(t testing.TB, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	root := path.Join("fixtures", name)
	err := fs.WalkDir(beprotocol.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := beprotocol.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// Write writes content to dir/rel, creating parent directories.
func Write(t testing.TB, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Read returns the content of dir/rel.
func Read(t testing.TB, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
