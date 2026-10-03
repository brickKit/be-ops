package yamlblock

import "testing"

const openapi = `openapi: 3.1.0
paths:
  /a:
    get: {operationId: a}

  /b:
    get: {operationId: b}

components: {}
`

const (
	begin = "# >>> gen"
	end   = "# <<< gen"
)

func TestReplaceRegion_AppendsAtTheEndOfTheBlock(t *testing.T) {
	got, err := ReplaceRegion([]byte(openapi), "paths", begin, end, "  /c:\n    get: {operationId: c}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := `openapi: 3.1.0
paths:
  /a:
    get: {operationId: a}

  /b:
    get: {operationId: b}
  # >>> gen
  /c:
    get: {operationId: c}
  # <<< gen

components: {}
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := ReplaceRegion(got, "paths", begin, end, "  /c:\n    get: {operationId: c}\n")
	if err != nil || string(again) != want {
		t.Fatalf("not idempotent: %v\n%s", err, again)
	}
	removed, err := ReplaceRegion(got, "paths", begin, end, "")
	if err != nil || string(removed) != openapi {
		t.Fatalf("an empty region removes it: %v\n%s", err, removed)
	}
}

func TestReplaceRegion_Refusals(t *testing.T) {
	if _, err := ReplaceRegion([]byte("paths: {}\n"), "paths", begin, end, "  /c: {}\n"); err == nil {
		t.Error("a flow-style block cannot take a region")
	}
	if _, err := ReplaceRegion([]byte("paths:\n  # >>> gen\n  /c: {}\n"), "paths", begin, end, ""); err == nil {
		t.Error("a begin marker without its end marker is refused")
	}
	if got, err := ReplaceRegion([]byte("openapi: 3.1.0\n"), "paths", begin, end, ""); err != nil || string(got) != "openapi: 3.1.0\n" {
		t.Errorf("no block and no region: unchanged, got %q %v", got, err)
	}
	if got, err := ReplaceRegion([]byte("openapi: 3.1.0\n"), "paths", begin, end, "  /c: {}\n"); err != nil ||
		string(got) != "openapi: 3.1.0\n\npaths:\n  # >>> gen\n  /c: {}\n  # <<< gen\n" {
		t.Errorf("no block: appended, got %q %v", got, err)
	}
}

func TestRegion_ReturnsTheGeneratedText(t *testing.T) {
	src, _ := ReplaceRegion([]byte(openapi), "paths", begin, end, "  /c: {}\n")
	if got := Region(src, "paths", begin, end); got != "  /c: {}\n" {
		t.Errorf("region = %q", got)
	}
	if got := Region([]byte(openapi), "paths", begin, end); got != "" {
		t.Errorf("no region = %q", got)
	}
}
