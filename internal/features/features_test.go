package features

import (
	"testing"
)

func TestTheDocumentListsTheSelectedComponentsInOrder(t *testing.T) {
	d, err := FromManifest([]byte(`project: p
sources: [{name: a, type: local, path: ./c}]
components:
  - id: mdm/product
    version: 3.0.16
  - id: infra/authz
    version: 3.0.15
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Project != "p" || len(d.Components) != 2 || d.Components[0] != (Component{"infra/authz", "3.0.15"}) ||
		d.Components[1] != (Component{"mdm/product", "3.0.16"}) {
		t.Errorf("got %+v", d)
	}
	for _, bad := range []string{"components: [{id: a/b}]", "components: [{id: a/b, version: 1}, {id: a/b, version: 2}]"} {
		if _, err := FromManifest([]byte(bad)); err == nil {
			t.Errorf("%q is refused", bad)
		}
	}
}
