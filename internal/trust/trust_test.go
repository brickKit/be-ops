package trust

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/stretchr/testify/require"
)

// fakeS3 is an object store as the run profile uses it: conditional create, list with LastModified, get. Its clock
// is the receipt time (P12.7); down makes every request fail.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	at      map[string]time.Time
	now     time.Time
	down    bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "unreachable", http.StatusServiceUnavailable)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/be-run-profile/")
	switch {
	case r.Method == http.MethodPut:
		if _, ok := f.objects[key]; ok {
			http.Error(w, "exists", http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.objects[key], f.at[key] = body, f.now
	case r.URL.Query().Get("list-type") == "2":
		type content struct {
			Key          string
			LastModified string
		}
		var page struct {
			XMLName  xml.Name  `xml:"ListBucketResult"`
			Contents []content `xml:"Contents"`
		}
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			page.Contents = append(page.Contents, content{Key: k, LastModified: f.at[k].UTC().Format(time.RFC3339Nano)})
		}
		_ = xml.NewEncoder(w).Encode(page)
	default:
		body, ok := f.objects[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}
}

func (f *fakeS3) set(t time.Time, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now, f.down = t, down
}

type fixture struct {
	*Issuer
	s3  *fakeS3
	dir string
	t0  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t0 := time.Now().Truncate(time.Second)
	s3 := &fakeS3{objects: map[string][]byte{}, at: map[string]time.Time{}, now: t0}
	srv := httptest.NewServer(s3)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	i := &Issuer{Deployment: "t13", Dir: filepath.Join(dir, "trust"), Bundle: filepath.Join(dir, "tls", "ca.pem"),
		Now: func() time.Time { return t0 }, Out: io.Discard}
	st, err := runprofile.New(runprofile.Options{Endpoint: srv.URL, Bucket: "be-run-profile", AccessKey: "k",
		Secret: func() string { return "s" }, Deployment: "t13", Writer: runprofile.Writer{Component: Writer, Instance: "x"},
		Identity: bundleIdentity{i}})
	require.NoError(t, err)
	i.Profile = st
	return &fixture{Issuer: i, s3: s3, dir: dir, t0: t0}
}

// cred issues a credential and answers its certificate (DER).
func (f *fixture) cred(t *testing.T, component string) []byte {
	t.Helper()
	_, err := f.Issue(context.Background(), component, "component", filepath.Join(f.dir, "tls"),
		strings.ReplaceAll(component, "/", "-"), 365)
	require.NoError(t, err)
	return certDER(t, filepath.Join(f.dir, "tls", strings.ReplaceAll(component, "/", "-")+".cert.pem"))
}

func certDER(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	b, _ := pem.Decode(raw)
	require.NotNil(t, b)
	return b.Bytes
}

func (f *fixture) history(t *testing.T) *runprofile.TrustHistory {
	t.Helper()
	h, err := f.Profile.Trust(context.Background())
	require.NoError(t, err)
	return h
}

func (f *fixture) bundle(t *testing.T) []string {
	t.Helper()
	roots, err := readBundle(f.Bundle)
	require.NoError(t, err)
	var fps []string
	for _, c := range roots {
		fps = append(fps, runprofile.Fingerprint(c.Raw))
	}
	return fps
}

func TestACredentialIsPublishedOnlyOnceItsRecordIsTaken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	_, ca, err := f.current()
	require.NoError(t, err)
	require.Equal(t, []string{runprofile.Fingerprint(ca.Raw)}, f.bundle(t), "the CA is the trust root")

	der := f.cred(t, "infra/authz")
	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	require.Equal(t, "spiffe://t13/component/infra/authz", leaf.URIs[0].String())
	require.Equal(t, []string{"infra.authz.component.t13"}, leaf.DNSNames)
	require.NoError(t, f.history(t).Holds([][]byte{der}, f.t0.Add(time.Minute)), "a credential of the CA holds")
	require.Empty(t, f.history(t).Problems)

	f.s3.set(f.t0, true) // the run profile does not take the record: nothing is written (P12.27)
	_, err = f.Issue(ctx, "infra/audit", "component", filepath.Join(f.dir, "tls"), "infra-audit", 365)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(f.dir, "tls", "infra-audit.cert.pem"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, statErr = os.Stat(filepath.Join(f.dir, "tls", "infra-audit.key.pem"))
	require.ErrorIs(t, statErr, os.ErrNotExist)

	require.Error(t, f.Init(ctx, 3650), "a deployment has one CA: rotate makes the next")
}

func TestARotatedCAStaysTrustedThroughTheRotationRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	_, old, err := f.current()
	require.NoError(t, err)
	oldFP := runprofile.Fingerprint(old.Raw)
	before := f.cred(t, "infra/authz")

	f.s3.set(f.t0, true) // a rotation the run profile does not take changes nothing
	require.Error(t, f.Rotate(ctx, 3650))
	_, still, err := f.current()
	require.NoError(t, err)
	require.Equal(t, oldFP, runprofile.Fingerprint(still.Raw))
	require.Equal(t, []string{oldFP}, f.bundle(t))

	f.s3.set(f.t0.Add(time.Minute), false)
	require.NoError(t, f.Rotate(ctx, 3650))
	_, next, err := f.current()
	require.NoError(t, err)
	nextFP := runprofile.Fingerprint(next.Raw)
	require.NotEqual(t, oldFP, nextFP)
	require.Equal(t, []string{oldFP, nextFP}, f.bundle(t), "both CAs during the acceptance window")
	after := f.cred(t, "infra/authz")
	afterLeaf, err := x509.ParseCertificate(after)
	require.NoError(t, err)
	require.Equal(t, next.Subject.String(), afterLeaf.Issuer.String(), "issued by the next CA from now on")

	require.Error(t, f.Retire(nextFP), "the current CA is not retired")
	require.NoError(t, f.Retire(oldFP))
	require.Equal(t, []string{nextFP}, f.bundle(t))
	h := f.history(t) // the old CA is no longer a root: trusted through the doubly signed rotation (P12.25)
	require.True(t, h.Trusted(oldFP))
	require.NoError(t, h.Holds([][]byte{before}, f.t0.Add(2*time.Minute)))
	require.NoError(t, h.Holds([][]byte{after}, f.t0.Add(2*time.Minute)))
	require.Empty(t, h.Problems)
}

func TestARevokedCredentialSignsNothingReceivedAfter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	der := f.cred(t, "infra/authz")
	revokedAt := f.t0.Add(10 * time.Minute)
	f.s3.set(revokedAt, false)
	require.NoError(t, f.Revoke(ctx, runprofile.Fingerprint(der), false))

	h := f.history(t)
	require.NoError(t, h.Holds([][]byte{der}, revokedAt.Add(-3*time.Second)), "received before the revocation")
	require.Error(t, h.Holds([][]byte{der}, revokedAt.Add(-time.Second)), "within the declared skew: refused")
	require.Error(t, h.Holds([][]byte{der}, revokedAt.Add(time.Minute)), "received after")
	other := f.cred(t, "infra/audit")
	require.NoError(t, h.Holds([][]byte{other}, revokedAt.Add(time.Minute)), "another credential of the CA holds")
}

func TestARevocationStandsWhileTheRunProfileIsUnreachable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	_, old, err := f.current()
	require.NoError(t, err)
	oldFP := runprofile.Fingerprint(old.Raw)
	before := f.cred(t, "infra/authz")
	f.s3.set(f.t0.Add(time.Minute), false)
	require.NoError(t, f.Rotate(ctx, 3650)) // security 02: an emergency revocation rotates out a next chain first

	f.s3.set(f.t0.Add(2*time.Minute), true)
	err = f.Revoke(ctx, oldFP, true)
	require.ErrorContains(t, err, "the revocation stands")
	_, next, err := f.current()
	require.NoError(t, err)
	require.Equal(t, []string{runprofile.Fingerprint(next.Raw)}, f.bundle(t), "the CA left the trust root at once")
	pending, err := os.ReadDir(filepath.Join(f.Dir, "pending"))
	require.NoError(t, err)
	require.Len(t, pending, 1)

	flushedAt := f.t0.Add(5 * time.Minute)
	f.s3.set(flushedAt, false)
	n, err := f.Flush(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	pending, err = os.ReadDir(filepath.Join(f.Dir, "pending"))
	require.NoError(t, err)
	require.Empty(t, pending)
	h := f.history(t)
	require.NoError(t, h.Holds([][]byte{before}, f.t0.Add(3*time.Minute)), "received before the record was written")
	require.Error(t, h.Holds([][]byte{before}, flushedAt.Add(time.Second)), "a credential of the revoked CA")
}

func TestRevokingTheCurrentCALeavesTheDeploymentWithoutOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	_, ca, err := f.current()
	require.NoError(t, err)
	fp := runprofile.Fingerprint(ca.Raw)
	der := f.cred(t, "infra/authz")
	f.s3.set(f.t0.Add(time.Minute), false)
	require.NoError(t, f.Revoke(ctx, fp, true))
	_, _, err = f.current()
	require.Error(t, err, "no current CA")
	_, err = os.Stat(filepath.Join(f.Dir, "ca.key.pem.revoked"))
	require.NoError(t, err)
	require.Error(t, f.Revoke(ctx, fp, true), "nothing signs a revocation without a CA")

	require.NoError(t, f.Init(ctx, 3650)) // a new CA, a new trust root
	h := f.history(t)
	require.False(t, h.Trusted(fp), "the revoked CA is no root and no rotation leads to it")
	require.Error(t, h.Holds([][]byte{der}, f.t0.Add(2*time.Minute)))
	fresh := f.cred(t, "infra/authz")
	require.NoError(t, h.Holds([][]byte{fresh}, f.t0.Add(2*time.Minute)))
}

func TestAForgedRotationIsNotUsed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.Init(ctx, 3650))
	// someone with the store's write credential but not the CA's key writes a rotation to their own CA: signed by
	// that CA, which the history does not trust
	other := &Issuer{Deployment: f.Deployment, Dir: filepath.Join(f.dir, "other"), Bundle: filepath.Join(f.dir, "other.pem"),
		Profile: f.Profile, Now: f.Now, Out: io.Discard}
	require.NoError(t, other.Init(ctx, 3650))
	require.NoError(t, other.Rotate(ctx, 3650))
	_, forged, err := other.current()
	require.NoError(t, err)
	h := f.history(t)
	require.False(t, h.Trusted(runprofile.Fingerprint(forged.Raw)))
	var buf bytes.Buffer
	f.Out = &buf
	require.NoError(t, f.History(ctx))
	require.Contains(t, buf.String(), "root ")
}
