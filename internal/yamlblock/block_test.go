package yamlblock

import "testing"

const src = `# head
a: 1

events:
  publishes:
    - x.y.z.v1

  subscribes: []

# head of b
b:
  c: 2
`

func TestReplaceTopLevel_KeepsNeighboursAndTheirComments(t *testing.T) {
	got := string(ReplaceTopLevel([]byte(src), "events", "events:\n  publishes: [n.m.o.v1]\n"))
	want := `# head
a: 1

events:
  publishes: [n.m.o.v1]

# head of b
b:
  c: 2
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceTopLevel_AppendsWhenAbsent(t *testing.T) {
	got := string(ReplaceTopLevel([]byte("a: 1\n"), "events", "events:\n  publishes: []\n"))
	if got != "a: 1\n\nevents:\n  publishes: []\n" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceTopLevel_RemovesWithEmptyBlock(t *testing.T) {
	got := string(ReplaceTopLevel([]byte(src), "events", ""))
	want := "# head\na: 1\n\n# head of b\nb:\n  c: 2\n"
	if got != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestReplaceTopLevel_Idempotent(t *testing.T) {
	once := ReplaceTopLevel([]byte(src), "events", "events:\n  publishes: [n.m.o.v1]\n")
	twice := ReplaceTopLevel(once, "events", "events:\n  publishes: [n.m.o.v1]\n")
	if string(once) != string(twice) {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestReplaceTopLevel_LastBlockWithoutTrailingNewline(t *testing.T) {
	got := string(ReplaceTopLevel([]byte("a: 1\nevents:\n  publishes: []"), "events", "events:\n  publishes: [q.w.e.v1]\n"))
	if got != "a: 1\nevents:\n  publishes: [q.w.e.v1]\n" {
		t.Errorf("got %q", got)
	}
}
