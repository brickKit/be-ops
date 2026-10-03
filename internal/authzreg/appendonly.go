package authzreg

import "fmt"

// AppendOnly compares a newer permissions.tsv with an older one (normally the committed version)
// and returns one line per change the append-only rule forbids:
//
//   - the old header must be a prefix of the new one (columns are only appended);
//   - every old key is still there (a key is retired with deprecated, never removed);
//   - an old cell that is not empty keeps its value (type, owner_component, a deprecated
//     tombstone, a stated delegable); an empty one may be filled. title is display text and may
//     change.
func AppendOnly(base, next []byte) ([]string, error) {
	oldRows, err := parsePermissions("base", base)
	if err != nil {
		return nil, err
	}
	oldHeader, _ := splitTable(base)
	newHeader, _ := splitTable(next)
	var out []string
	if len(newHeader) < len(oldHeader) {
		out = append(out, fmt.Sprintf("header %q drops columns of %q (columns are only appended)", newHeader, oldHeader))
	}
	for i := 0; i < len(oldHeader) && i < len(newHeader); i++ {
		if oldHeader[i] != newHeader[i] {
			out = append(out, fmt.Sprintf("header column %d is %q, was %q (columns are only appended)", i+1, newHeader[i], oldHeader[i]))
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	newRows, err := parsePermissions("next", next)
	if err != nil {
		return nil, err
	}
	byKey := map[string]PermissionRow{}
	for _, r := range newRows {
		byKey[r.Key] = r
	}
	for _, o := range oldRows {
		n, ok := byKey[o.Key]
		if !ok {
			out = append(out, fmt.Sprintf("%s was removed (retire a key with deprecated, never remove it)", o.Key))
			continue
		}
		for _, f := range []struct{ name, was, is string }{
			{"type", o.Type, n.Type}, {"owner_component", o.OwnerComponent, n.OwnerComponent},
			{"deprecated", o.Deprecated, n.Deprecated}, {"delegable", o.Delegable, n.Delegable},
		} {
			if f.was != "" && f.was != f.is {
				out = append(out, fmt.Sprintf("%s: %s was %q, is %q (a released value never changes)", o.Key, f.name, f.was, f.is))
			}
		}
	}
	return out, nil
}
