package configschema

import (
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

// The profile trigger keys as decided for rc.2 (stageB-review O1): db when configSchema declares
// PG_SCHEMA or PG_HOST, blob when it declares S3_BUCKET or S3_URL; either key alone is enough.
func TestProfiles_TriggerKeys(t *testing.T) {
	cases := []struct {
		keys    string
		db, blb bool
	}{
		{"", false, false},
		{"    PG_SCHEMA: {type: string}\n", true, false},
		{"    PG_HOST: {type: string}\n", true, false},
		{"    PG_DATABASE: {type: string}\n", false, false},
		{"    S3_BUCKET: {type: string}\n", false, true},
		{"    S3_URL: {type: string}\n", false, true},
		{"    S3_REGION: {type: string}\n", false, false},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		testfixture.Write(t, dir, "component.yaml", "metadata: {id: erp/x, version: 3.0.0}\nconfigSchema:\n  type: object\n  properties:\n"+tc.keys+"    X_OWN: {type: string}\n")
		cat, c := setup(t, dir)
		ps, _, err := Profiles(cat, c)
		if err != nil {
			t.Fatal(err)
		}
		got := "," + strings.Join(ps, ",") + ","
		if strings.Contains(got, ",db,") != tc.db || strings.Contains(got, ",jobs,") != tc.db || strings.Contains(got, ",lifecycle,") != tc.db ||
			strings.Contains(got, ",blob,") != tc.blb {
			t.Errorf("keys %q: profiles %v, want db=%v blob=%v", tc.keys, ps, tc.db, tc.blb)
		}
	}
}
