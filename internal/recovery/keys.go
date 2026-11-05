package recovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Step 6, 6.8 (assembly protocol 15): the identity member signs with a newly made key and publishes a new standby
// (the public key set restored with the database may hold a key that leaked). Its keys are deployment secret files
// (IAM_SIGNING_KEY, IAM_SIGNING_KEY_NEXT, file:// in its configuration): they do not go back with the database, so a
// key revoked after T stays revoked; the step writes two new keys into those files, the old ones kept beside them
// (".before-<recovery>"), and the member re-reads its secret files. Its public key set is on the user surface, which
// answers MAINTENANCE until the deployment reopens.

const (
	keyCurrent = "IAM_SIGNING_KEY"
	keyNext    = "IAM_SIGNING_KEY_NEXT"
)

// Keys is 6.8 for every component whose configuration names a signing key.
func (t *Tool) Keys(ctx context.Context) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepKeys); err != nil {
		return err
	}
	suffix := ".before-" + strings.NewReplacer(":", "", " ", "_", "/", "_").Replace(st.Recovery)
	done := map[string]any{}
	for _, c := range t.Project.Components {
		path := filepath.Join(t.Project.Dir, "config", strings.ReplaceAll(c.ID, "/", "-")+".yaml")
		cfg := map[string]any{}
		if readYAML(path, &cfg) != nil || cfg[keyCurrent] == nil {
			continue
		}
		cur, ok := fileRef(cfg[keyCurrent])
		if !ok {
			return fmt.Errorf("%s: %s is not a file:// reference; make its new key by hand", c.ID, keyCurrent)
		}
		next, ok := fileRef(cfg[keyNext])
		if cfg[keyNext] != nil && !ok {
			return fmt.Errorf("%s: %s is not a file:// reference; make its new key by hand", c.ID, keyNext)
		}
		if !ok { // no standby yet: one beside the current key, named in the configuration
			next = filepath.Join(filepath.Dir(cur), "signing-key-next.pem")
			if err := appendLine(path, keyNext+": file://"+next); err != nil {
				return err
			}
		}
		var kids []string
		for _, rel := range []string{cur, next} {
			kid, err := newKey(filepath.Join(t.Project.Dir, rel), suffix)
			if err != nil {
				return fmt.Errorf("%s: %w", c.ID, err)
			}
			kids = append(kids, kid)
		}
		done[c.ID] = map[string]string{"current": kids[0], "standby": kids[1]}
		t.say("%s: a new signing key %s and standby %s (the old files kept as *%s)", c.ID, kids[0], kids[1], suffix)
	}
	if len(done) == 0 {
		return fmt.Errorf("no component's configuration names %s", keyCurrent)
	}
	return t.record(ctx, st, Step{Step: StepKeys, Detail: map[string]any{"keys": done}})
}

var fileValue = regexp.MustCompile(`^file://(.+)$`)

// fileRef is the project-relative path of a file:// value.
func fileRef(v any) (string, bool) {
	s, _ := v.(string)
	m := fileValue.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// newKey keeps an existing file beside it and writes a new Ed25519 key (PKCS #8 PEM, 0600); it answers the key's kid
// (the RFC 7638 thumbprint of its public JWK, as the identity member computes it).
func newKey(path, suffix string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+suffix); err != nil {
			return "", err
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return "", err
	}
	x := base64.RawURLEncoding.EncodeToString(pub)
	sum := sha256.Sum256([]byte(`{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func appendLine(path, line string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(raw)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return os.WriteFile(path, []byte(s+line+"\n"), 0o644)
}
