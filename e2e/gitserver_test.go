//go:build e2e && linux

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	mathrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// gitAddr is fixed so Git source locators, and therefore locks and plans, are
// identical on every run.
const gitAddr = "localhost:8443"

// gitBase is the URL prefix of the fixture repositories served over HTTPS.
const gitBase = "https://" + gitAddr + "/"

// fixtureTags are created on every fixture repository.
var fixtureTags = []string{"v1", "v1.0.0"}

// fixtureCommitEnv makes fixture commits, and so their revisions, reproducible.
var fixtureCommitEnv = []string{
	"GIT_AUTHOR_NAME=agnthub e2e",
	"GIT_AUTHOR_EMAIL=e2e@agnthub.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_NAME=agnthub e2e",
	"GIT_COMMITTER_EMAIL=e2e@agnthub.invalid",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=/dev/null",
}

// publishFixtures copies every fixture tree to root/src/<name> (the local
// sources) and publishes each as a bare repository under root/git/<name>.git.
// The fixture tags name its first commit; a tree under testdata/revisions/<name>
// is laid over it as a second commit on main, so a tag can later be moved to
// main (moveTag) to publish a newer revision of the source.
func publishFixtures(t *testing.T, fixtures, root string) {
	t.Helper()
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		work := filepath.Join(root, "src", name)
		copyTree(t, filepath.Join(fixtures, name), work)
		if name == largeFixture {
			writeLargeContent(t, work)
		}
		git(t, work, "init", "--quiet", "--initial-branch=main")
		git(t, work, "-c", "core.autocrlf=false", "add", "--all")
		git(t, work, "commit", "--quiet", "--no-gpg-sign", "-m", "fixture "+name)
		for _, tag := range fixtureTags {
			git(t, work, "tag", tag)
		}
		revision := filepath.Join(filepath.Dir(fixtures), "revisions", name)
		if _, err := os.Stat(revision); err == nil {
			copyTree(t, revision, work)
			git(t, work, "-c", "core.autocrlf=false", "add", "--all")
			git(t, work, "commit", "--quiet", "--no-gpg-sign", "-m", "fixture "+name+" revision 2")
		}
		bare := filepath.Join(root, "git", name+".git")
		git(t, root, "clone", "--quiet", "--bare", work, bare)
		// The local source is the plain directory, not a checkout.
		if err := os.RemoveAll(filepath.Join(work, ".git")); err != nil {
			t.Fatal(err)
		}
	}
}

// moveTag points tag of the published repository name at main, the revision
// after the one the tag names, and restores it when the test ends.
func moveTag(t *testing.T, root, name, tag string) {
	t.Helper()
	bare := filepath.Join(root, "git", name+".git")
	git(t, bare, "tag", "--force", tag, "main")
	t.Cleanup(func() { git(t, bare, "tag", "--force", tag, "main~1") })
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + processEnv.Get("PATH"), "HOME=" + dir}, fixtureCommitEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// copyTree copies regular files and directories with normalized permissions, so
// the fixture bytes and modes do not depend on the checkout.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, content, 0o644)
		default:
			return errors.New("fixture contains a non-regular file: " + rel)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// serveGit serves root/git over smart HTTP and TLS with git http-backend, and
// returns the PEM file of the self-signed certificate for GIT_SSL_CAINFO. Like
// GitHub, the server accepts partial-clone filters and wants of any object;
// uploads records what each fetch sent.
func serveGit(t *testing.T, root string) string {
	t.Helper()
	certPEM, keyPEM := selfSigned(t)
	caFile := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	execPath, err := exec.Command(backend, "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	handler := &cgi.Handler{
		Path: filepath.Join(string(trimNewline(execPath)), "git-http-backend"),
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Join(root, "git"),
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=uploadpack.allowFilter",
			"GIT_CONFIG_VALUE_0=true",
			"GIT_CONFIG_KEY_1=uploadpack.allowAnySHA1InWant",
			"GIT_CONFIG_VALUE_1=true",
		},
	}
	server := &http.Server{
		Handler: uploads.record(handler),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{pair},
			MinVersion:   tls.VersionTLS12,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	var listeners []net.Listener
	for _, addr := range []string{"127.0.0.1:8443", "[::1]:8443"} {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue // no IPv6 loopback in this container
		}
		listeners = append(listeners, listener)
		go func() { _ = server.ServeTLS(listener, "", "") }()
	}
	if len(listeners) == 0 {
		t.Fatal("cannot listen on " + gitAddr)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return caFile
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// largeFixture is the fixture repository whose whole fetch is too large: next
// to its agent it holds largeOtherBytes of files that are not agents and a
// binary above the CLI's per-file limit, generated here (deterministically, so
// its revision and the goldens are stable).
const (
	largeFixture    = "large"
	largeFiles      = 2560
	largeFileBytes  = 8 << 10
	largeOtherBytes = largeFiles * largeFileBytes // 20 MiB, above the CLI's 16 MiB
	largeBinary     = 4 << 20
	// largeSparseMax bounds what the sparse fetches of the large fixture may
	// send: its trees and the files discovery reads, far below largeOtherBytes.
	largeSparseMax = 1 << 20
)

func writeLargeContent(t *testing.T, work string) {
	t.Helper()
	noise := mathrand.NewChaCha8([32]byte{'a', 'g', 'n', 't', 'h', 'u', 'b'})
	write := func(rel string, size int) {
		data := make([]byte, size)
		_, _ = noise.Read(data)
		path := filepath.Join(work, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("assets/model.bin", largeBinary)
	for i := range largeFiles {
		write(fmt.Sprintf("vendor/%02d/blob-%04d.dat", i%64, i), largeFileBytes)
	}
}

// uploadCounter records the bytes the fake server sent for fetches of the
// large fixture, by the filter the fetch asked for: whole (blob:limit) or
// sparse (blob:none: the trees, then files by id).
type uploadCounter struct {
	whole, sparse atomic.Int64
}

var uploads uploadCounter

func (u *uploadCounter) reset() {
	u.whole.Store(0)
	u.sparse.Store(0)
}

func (u *uploadCounter) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			!strings.HasPrefix(r.URL.Path, "/"+largeFixture+".git/git-upload-pack") {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		request := body
		if r.Header.Get("Content-Encoding") == "gzip" {
			if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
				request, _ = io.ReadAll(zr)
			}
		}
		counted := &countingWriter{ResponseWriter: w}
		next.ServeHTTP(counted, r)
		switch {
		case bytes.Contains(request, []byte("filter blob:none")):
			u.sparse.Add(counted.n)
		case bytes.Contains(request, []byte("filter blob:limit")):
			u.whole.Add(counted.n)
		}
	})
}

// requireSparse fails unless the last fetches of the large fixture were
// sparse and sent only a small fraction of the repository.
func (u *uploadCounter) requireSparse(t *testing.T) {
	t.Helper()
	sparse, whole := u.sparse.Load(), u.whole.Load()
	t.Logf("large fixture: sparse fetches sent %d bytes, the abandoned whole fetch %d",
		sparse, whole)
	if sparse == 0 {
		t.Errorf("the large fixture was not fetched sparsely")
	}
	if sparse > largeSparseMax {
		t.Errorf("sparse fetches sent %d bytes, more than %d: files that are not "+
			"candidates were fetched", sparse, largeSparseMax)
	}
}

type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

func (c *countingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
