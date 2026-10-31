//go:build e2e && linux

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
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
		git(t, work, "init", "--quiet", "--initial-branch=main")
		git(t, work, "-c", "core.autocrlf=false", "add", "--all")
		git(t, work, "commit", "--quiet", "--no-gpg-sign", "-m", "fixture "+name)
		for _, tag := range fixtureTags {
			git(t, work, "tag", tag)
		}
		bare := filepath.Join(root, "git", name+".git")
		git(t, root, "clone", "--quiet", "--bare", work, bare)
		// The local source is the plain directory, not a checkout.
		if err := os.RemoveAll(filepath.Join(work, ".git")); err != nil {
			t.Fatal(err)
		}
	}
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
// returns the PEM file of the self-signed certificate for GIT_SSL_CAINFO.
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
		},
	}
	server := &http.Server{
		Handler: handler,
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
