// Package trust is the deployment trust domain's issuer (be-protocol P12.24-P12.27, assembly protocol 15's trust
// material, security 02's credential lifecycle): it keeps the CA's private key, makes the trust root, issues the
// service credentials, rotates the CA and revokes, and writes each of these into the run profile, signed with the CA's
// own key. The run profile is the trust material's history (A148): it is outside the deployment and object-locked,
// so it never goes back with a restore, and step 6.2 of a full recovery judges every signed record against it at the
// time the store received the record.
//
// Order (P12.27): an issued credential or a new CA is written to its files only after the run profile took its record;
// a revocation is never put off: it takes effect first (a CA leaves the trust root), then its record is written, and a
// record the run profile did not take waits in the issuer's pending directory (flush writes it) while the command
// fails loudly.
package trust

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/runprofile"
)

// Writer is the issuer's name in the run profile's records (the records are signed by the CA, not by a service
// credential).
const Writer = "ops/trust"

// Profile is what the issuer needs of the run profile (runprofile.Store).
type Profile interface {
	AppendWith(ctx context.Context, kind string, body any, key crypto.Signer, chain [][]byte) (runprofile.Record, error)
	Trust(ctx context.Context) (*runprofile.TrustHistory, error)
}

// Issuer is one deployment's issuer: its files and its run profile.
type Issuer struct {
	Deployment string
	Dir        string // the issuer's secrets: ca.key.pem, ca.cert.pem, pending/ (never mounted into a component)
	Bundle     string // the deployment's trust root (SERVICE_TLS_CA_FILE): the CA certificates the components trust
	Profile    Profile
	Now        func() time.Time
	Out        io.Writer
}

func (i *Issuer) keyFile() string  { return filepath.Join(i.Dir, "ca.key.pem") }
func (i *Issuer) certFile() string { return filepath.Join(i.Dir, "ca.cert.pem") }
func (i *Issuer) pending() string  { return filepath.Join(i.Dir, "pending") }

func (i *Issuer) say(format string, args ...any) { fmt.Fprintf(i.Out, format+"\n", args...) }

// Init makes the deployment's first CA (or a new one after the current was revoked): its anchor record goes into the
// run profile, then its key and certificate into the issuer's directory and its certificate into the trust root.
func (i *Issuer) Init(ctx context.Context, days int) error {
	if _, err := os.Stat(i.keyFile()); err == nil {
		return fmt.Errorf("%s exists: this deployment has a CA (rotate makes the next one)", i.keyFile())
	}
	key, cert, err := i.newCA(days)
	if err != nil {
		return err
	}
	body := runprofile.TrustBody{Event: runprofile.EventAnchor, CA: pemCert(cert.Raw)}
	if _, err := i.Profile.AppendWith(ctx, runprofile.KindTrustCA, body, key, [][]byte{cert.Raw}); err != nil {
		return fmt.Errorf("the run profile did not take the anchor, nothing published: %w", err)
	}
	if err := i.writeCA(key, cert); err != nil {
		return err
	}
	if err := i.addToBundle(cert); err != nil {
		return err
	}
	i.say("✓ CA %s (anchor recorded), trust root %s", runprofile.Fingerprint(cert.Raw), i.Bundle)
	return nil
}

// Issue makes a service credential for a component (P8.5: its SPIFFE ID and the derived DNS name) and writes it as
// <out>/<name>.cert.pem and .key.pem once its issue record is in the run profile.
func (i *Issuer) Issue(ctx context.Context, component, role, out, name string, days int) (string, error) {
	caKey, ca, err := i.current()
	if err != nil {
		return "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(identity.SPIFFE(i.Deployment, component))
	if err != nil {
		return "", err
	}
	now := i.Now()
	tmpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: component}, URIs: []*url.URL{u},
		DNSNames: []string{identity.DNSName(i.Deployment, component)}, NotBefore: now.Add(-time.Hour),
		NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	fp := runprofile.Fingerprint(der)
	body := runprofile.TrustBody{Event: runprofile.EventIssue, Fingerprint: fp, Component: component, Role: role,
		NotAfter: tmpl.NotAfter.UTC().Format(time.RFC3339)}
	if _, err := i.Profile.AppendWith(ctx, runprofile.KindTrustCredential, body, caKey, [][]byte{ca.Raw}); err != nil {
		return "", fmt.Errorf("the run profile did not take the issue record, nothing published: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(out, name)
	if err := writeAtomic(base+".key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return "", err
	}
	if err := writeAtomic(base+".cert.pem", []byte(pemCert(der)), 0o644); err != nil {
		return "", err
	}
	i.say("✓ %s: credential %s (role %s, until %s) in %s.{cert,key}.pem", component, fp, role, body.NotAfter, base)
	return fp, nil
}

// Rotate makes the next CA: the rotation record is signed by the current CA and accepted by the next (P12.24); once
// the run profile took it, the next CA becomes the current one (the previous kept beside it as *.before-<fp>) and joins
// the trust root, where the previous stays until Retire (security 02: the acceptance window).
func (i *Issuer) Rotate(ctx context.Context, days int) error {
	prevKey, prev, err := i.current()
	if err != nil {
		return err
	}
	key, cert, err := i.newCA(days)
	if err != nil {
		return err
	}
	body := runprofile.TrustBody{Event: runprofile.EventRotation, CA: pemCert(cert.Raw),
		Previous: runprofile.Fingerprint(prev.Raw), Next: runprofile.Fingerprint(cert.Raw)}
	if body, err = runprofile.Accept(body, key); err != nil {
		return err
	}
	if _, err := i.Profile.AppendWith(ctx, runprofile.KindTrustCA, body, prevKey, [][]byte{prev.Raw}); err != nil {
		return fmt.Errorf("the run profile did not take the rotation, nothing published: %w", err)
	}
	suffix := ".before-" + body.Previous[:16]
	for _, f := range []string{i.keyFile(), i.certFile()} {
		if err := os.Rename(f, f+suffix); err != nil {
			return err
		}
	}
	if err := i.writeCA(key, cert); err != nil {
		return err
	}
	if err := i.addToBundle(cert); err != nil {
		return err
	}
	i.say("✓ CA %s → %s (rotation recorded); both in the trust root until retire --fingerprint %s", body.Previous,
		body.Next, body.Previous)
	return nil
}

// Retire takes a rotated-away CA out of the trust root after the acceptance window. It is not a revocation: what that
// CA signed before stays valid through the rotation record.
func (i *Issuer) Retire(fp string) error {
	_, cur, err := i.current()
	if err != nil {
		return err
	}
	if runprofile.Fingerprint(cur.Raw) == fp {
		return errors.New("that is the current CA: rotate first")
	}
	if err := i.removeFromBundle(fp); err != nil {
		return err
	}
	i.say("✓ CA %s retired from the trust root %s", fp, i.Bundle)
	return nil
}

// Revoke revokes a service credential (ca false) or a CA (ca true) by fingerprint. A CA leaves the trust root at once;
// revoking the current CA (security 02's emergency revocation when the run profile is unreachable and no next chain
// can be made) leaves the deployment without one: its files are kept as *.revoked and Init makes a new one. The record
// is signed by the current CA (by the revoked one itself when it is the current); a record the run profile did not
// take waits in pending/ and the error says so: the revocation stands.
func (i *Issuer) Revoke(ctx context.Context, fp string, ca bool) error {
	caKey, cur, err := i.current()
	if err != nil {
		return err
	}
	fp = strings.ToLower(fp)
	kind, body := runprofile.KindTrustCredential, runprofile.TrustBody{Event: runprofile.EventRevoke, Fingerprint: fp}
	if ca {
		kind = runprofile.KindTrustCA
		if err := i.removeFromBundle(fp); err != nil {
			return err
		}
		if runprofile.Fingerprint(cur.Raw) == fp {
			for _, f := range []string{i.keyFile(), i.certFile()} {
				if err := os.Rename(f, f+".revoked"); err != nil {
					return err
				}
			}
			i.say("⚠ the current CA is revoked: the deployment has none; init makes a new one, every credential is issued again")
		}
		i.say("✓ CA %s out of the trust root %s", fp, i.Bundle)
	} else {
		i.say("✓ credential %s revoked: issue its component a new one; the revoked one is refused from the history in a recovery", fp)
	}
	p := Pending{Kind: kind, Body: body, Key: pemKey(caKey), Chain: pemCert(cur.Raw)}
	if err := i.write(ctx, p); err != nil {
		if serr := i.spool(p); serr != nil {
			return fmt.Errorf("the revocation stands, but its record is neither in the run profile (%v) nor kept (%w)", err, serr)
		}
		return fmt.Errorf("⚠ the revocation stands; the run profile did not take its record (kept in %s: flush writes it): %w",
			i.pending(), err)
	}
	i.say("✓ revocation recorded")
	return nil
}

// Pending is a revocation record the run profile has not taken yet: what to write and the key it is signed with (the
// CA's, possibly a revoked one no longer in the issuer's directory).
type Pending struct {
	Kind  string               `json:"kind"`
	Body  runprofile.TrustBody `json:"body"`
	Key   string               `json:"key"`   // PEM, PKCS #8
	Chain string               `json:"chain"` // PEM
}

func (i *Issuer) write(ctx context.Context, p Pending) error {
	key, err := parseKey([]byte(p.Key))
	if err != nil {
		return err
	}
	cert, err := parseCert([]byte(p.Chain))
	if err != nil {
		return err
	}
	_, err = i.Profile.AppendWith(ctx, p.Kind, p.Body, key, [][]byte{cert.Raw})
	return err
}

func (i *Issuer) spool(p Pending) error {
	if err := os.MkdirAll(i.pending(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(i.pending(), fmt.Sprintf("%d.json", i.Now().UnixNano())), raw, 0o600)
}

// Flush writes the pending revocation records, oldest first; each written one is removed.
func (i *Issuer) Flush(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(i.pending())
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	n := 0
	for _, e := range entries {
		f := filepath.Join(i.pending(), e.Name())
		raw, err := os.ReadFile(f)
		if err != nil {
			return n, err
		}
		var p Pending
		if err := json.Unmarshal(raw, &p); err != nil {
			return n, fmt.Errorf("%s: %w", f, err)
		}
		if err := i.write(ctx, p); err != nil {
			return n, fmt.Errorf("%s: %w", f, err)
		}
		if err := os.Remove(f); err != nil {
			return n, err
		}
		n++
	}
	i.say("✓ %d pending record(s) written", n)
	return n, nil
}

// History prints the trust material as a recovery reads it: the CAs trusted from the current trust root and the
// records not used.
func (i *Issuer) History(ctx context.Context) error {
	h, err := i.Profile.Trust(ctx)
	if err != nil {
		return err
	}
	roots, err := readBundle(i.Bundle)
	if err != nil {
		return err
	}
	for _, c := range roots {
		i.say("root %s %s", runprofile.Fingerprint(c.Raw), c.Subject.CommonName)
	}
	for _, p := range h.Problems {
		i.say("not used: %s", p)
	}
	return nil
}

// current is the CA the issuer signs with.
func (i *Issuer) current() (crypto.Signer, *x509.Certificate, error) {
	rawKey, err := os.ReadFile(i.keyFile())
	if err != nil {
		return nil, nil, fmt.Errorf("the issuer's CA: %w (init makes one)", err)
	}
	key, err := parseKey(rawKey)
	if err != nil {
		return nil, nil, err
	}
	rawCert, err := os.ReadFile(i.certFile())
	if err != nil {
		return nil, nil, err
	}
	cert, err := parseCert(rawCert)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func (i *Issuer) newCA(days int) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := i.Now()
	name := pkix.Name{CommonName: i.Deployment + " CA " + now.UTC().Format("20060102T150405Z")}
	tmpl := &x509.Certificate{SerialNumber: serial(), Subject: name, Issuer: name, NotBefore: now.Add(-time.Hour),
		NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return key, cert, err
}

func (i *Issuer) writeCA(key *ecdsa.PrivateKey, cert *x509.Certificate) error {
	if err := os.MkdirAll(i.Dir, 0o700); err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeAtomic(i.keyFile(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return err
	}
	return writeAtomic(i.certFile(), []byte(pemCert(cert.Raw)), 0o644)
}

func (i *Issuer) addToBundle(cert *x509.Certificate) error {
	roots, err := readBundle(i.Bundle)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeBundle(i.Bundle, append(roots, cert))
}

func (i *Issuer) removeFromBundle(fp string) error {
	roots, err := readBundle(i.Bundle)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(slices.Clone(roots), func(c *x509.Certificate) bool { return runprofile.Fingerprint(c.Raw) == fp })
	if len(kept) == len(roots) {
		return fmt.Errorf("CA %s is not in the trust root %s", fp, i.Bundle)
	}
	return writeBundle(i.Bundle, kept)
}

func readBundle(path string) ([]*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []*x509.Certificate
	for rest := raw; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// writeBundle writes the trust root, readable: it is mounted into the components' containers, which run as other users.
func writeBundle(path string, certs []*x509.Certificate) error {
	var b strings.Builder
	for _, c := range certs {
		b.WriteString(pemCert(c.Raw))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}

// writeAtomic replaces a file whole (a component re-reading it never sees half of it).
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func pemCert(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func pemKey(k crypto.Signer) string {
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func parseKey(raw []byte) (crypto.Signer, error) {
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, errors.New("no PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("the key cannot sign")
	}
	return s, nil
}

func parseCert(raw []byte) (*x509.Certificate, error) {
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(b.Bytes)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
