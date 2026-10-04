// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain doubles as the far end of a fake ssh: the fake ssh script runs
// this binary with DM_FAKE_DIAL_STDIO=1, and it answers the Engine API's
// ping on stdin/stdout, as `docker system dial-stdio` would.
func TestMain(m *testing.M) {
	if os.Getenv("DM_FAKE_DIAL_STDIO") == "1" {
		serveStdio()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// pingHandler answers /_ping as a daemon does; nothing else.
func pingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Api-Version", "1.47")
		_, _ = io.WriteString(w, "OK") //nolint:errcheck // the client reads it or has gone
	})
}

func serveStdio() {
	conn := &stdioConn{done: make(chan struct{})}
	_ = http.Serve(&oneConnListener{conn: conn}, pingHandler()) //nolint:errcheck // ends when the client closes
}

type stdioConn struct {
	done chan struct{}
	once sync.Once
}

func (c *stdioConn) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (c *stdioConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}
func (c *stdioConn) LocalAddr() net.Addr              { return cmdAddr{} }
func (c *stdioConn) RemoteAddr() net.Addr             { return cmdAddr{} }
func (c *stdioConn) SetDeadline(time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(time.Time) error { return nil }

// oneConnListener hands out its one connection, then waits for it to close.
type oneConnListener struct {
	conn  *stdioConn
	taken bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if !l.taken {
		l.taken = true
		return l.conn, nil
	}
	<-l.conn.done
	return nil, errors.New("closed")
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return cmdAddr{} }

// isolate points every docker setting at an empty temp config directory.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	for _, k := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"} {
		t.Setenv(k, "")
	}
	return dir
}

// writeContext writes a context into the store as `docker context create`
// does: metadata under meta/, TLS files under tls/<digest>/docker/.
func writeContext(t *testing.T, dir, name, host string, skipVerify bool, files map[string][]byte) {
	t.Helper()
	meta := map[string]any{
		"Name":      name,
		"Metadata":  map[string]any{},
		"Endpoints": map[string]any{"docker": map[string]any{"Host": host, "SkipTLSVerify": skipVerify}},
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	metaDir := filepath.Join(dir, "contexts", "meta", contextDigest(name))
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", contextDigest(name), "docker")
	for file, body := range files {
		if err := os.MkdirAll(tlsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tlsDir, file), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// clientCert makes a self-signed client certificate and its key, in PEM.
func clientCert(t *testing.T) (certPEM, keyPEM []byte, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dockmaster-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), cert
}

// tlsDaemon is a daemon that speaks only TLS and only to a client holding
// the given certificate, like a dockerd run with --tlsverify.
func tlsDaemon(t *testing.T, client *x509.Certificate) (host string, caPEM []byte) {
	t.Helper()
	srv := httptest.NewUnstartedServer(pingHandler())
	pool := x509.NewCertPool()
	pool.AddCert(client)
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshakes are the point
	srv.StartTLS()
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return "tcp://" + srv.Listener.Addr().String(), ca
}

func ping(t *testing.T, c *Client) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Ping(ctx)
}

// TestContextTLS: a TLS context's certificates and SkipTLSVerify reach the
// client, by name (--context, :ctx) and by resolution (DOCKER_CONTEXT),
// with nothing exported (#33).
func TestContextTLS(t *testing.T) {
	certPEM, keyPEM, cert := clientCert(t)
	host, caPEM := tlsDaemon(t, cert)
	full := map[string][]byte{"ca.pem": caPEM, "cert.pem": certPEM, "key.pem": keyPEM}
	noCA := map[string][]byte{"cert.pem": certPEM, "key.pem": keyPEM}

	cases := []struct {
		files      map[string][]byte
		name       string
		wantErr    string
		skipVerify bool
		wantOK     bool
	}{
		{name: "ca, cert and key", files: full, wantOK: true},
		{name: "no ca verifies against the system roots", files: noCA, wantErr: "certificate"},
		{name: "no ca with SkipTLSVerify", files: noCA, skipVerify: true, wantOK: true},
		{name: "no TLS files is plain TCP", files: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolate(t)
			writeContext(t, dir, "remote", host, tc.skipVerify, tc.files)

			byName, err := NewForContext("remote", host)
			if err != nil {
				t.Fatal(err)
			}
			defer byName.Close() //nolint:errcheck // test client
			t.Setenv("DOCKER_CONTEXT", "remote")
			resolved, err := New("")
			if err != nil {
				t.Fatal(err)
			}
			defer resolved.Close() //nolint:errcheck // test client

			for how, c := range map[string]*Client{"NewForContext": byName, "DOCKER_CONTEXT": resolved} {
				err := ping(t, c)
				switch {
				case tc.wantOK && err != nil:
					t.Errorf("%s: ping: %v", how, err)
				case !tc.wantOK && err == nil:
					t.Errorf("%s: ping succeeded; want a failure", how)
				case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
					t.Errorf("%s: ping: %v; want it to mention %q", how, err, tc.wantErr)
				}
				want := []string{"--host", host}
				if tc.files != nil {
					want = []string{"--context", "remote"}
				}
				if got := c.EndpointArgs(); !slices.Equal(got, want) {
					t.Errorf("%s: EndpointArgs %q, want %q", how, got, want)
				}
			}
		})
	}
}

// TestNewForContextKeepsAnotherHost: a runtime profile named like a store
// context but dialing elsewhere gets its own host and no TLS of the
// context's; a name the store does not have is dialed as given.
func TestNewForContextKeepsAnotherHost(t *testing.T) {
	certPEM, keyPEM, _ := clientCert(t)
	dir := isolate(t)
	writeContext(t, dir, "colima", "tcp://10.0.0.1:2376", false, map[string][]byte{"cert.pem": certPEM, "key.pem": keyPEM})
	for _, tc := range []struct{ name, host string }{
		{name: "colima", host: "unix:///other/docker.sock"},
		{name: "nowhere", host: "unix:///nowhere/docker.sock"},
	} {
		c, err := NewForContext(tc.name, tc.host)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := c.EndpointArgs(), []string{"--host", tc.host}; !slices.Equal(got, want) {
			t.Errorf("%s: EndpointArgs %q, want %q", tc.name, got, want)
		}
		_ = c.Close() //nolint:errcheck // test client
	}
}

// fakeSSH puts an ssh on PATH that records its arguments and runs script.
func fakeSSH(t *testing.T, script string) (argsFile string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DM_FAKE_SSH_ARGS\"\n" + script + "\n"
	if err := os.WriteFile(
		filepath.Join(dir, "ssh"),
		[]byte(body),
		0o700,
	); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_FAKE_SSH_ARGS", argsFile)
	t.Setenv("DM_TEST_BIN", bin)
	return argsFile
}

// TestSSHContext: an ssh:// host runs `docker system dial-stdio` over ssh,
// as the CLI does, and the Engine API is spoken over that process (#34).
func TestSSHContext(t *testing.T) {
	isolate(t)
	argsFile := fakeSSH(t, `DM_FAKE_DIAL_STDIO=1 exec "$DM_TEST_BIN"`)
	host := "ssh://alice@build.example:2222/run/user/1000/docker.sock"
	c, err := New(host)
	if err != nil {
		t.Fatal(err)
	}
	if perr := ping(t, c); perr != nil {
		t.Fatalf("ping over ssh: %v", perr)
	}
	_ = c.Close()                     //nolint:errcheck // test client
	raw, err := os.ReadFile(argsFile) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-o", "ConnectTimeout=30", "-T", "-l", "alice", "-p", "2222", "--", "build.example",
		"docker", "--host", "unix:///run/user/1000/docker.sock", "system", "dial-stdio",
	}
	if got := strings.Fields(string(raw)); !slices.Equal(got, want) {
		t.Errorf("ssh args\n got %q\nwant %q", got, want)
	}
	if c.Host != host {
		t.Errorf("Host %q, want the ssh URL", c.Host)
	}
	if got := c.EndpointArgs(); !slices.Equal(got, []string{"--host", host}) {
		t.Errorf("EndpointArgs %q", got)
	}
}

// TestSSHFailureSaysWhy: ssh's own complaint reaches the error, not just
// "EOF".
func TestSSHFailureSaysWhy(t *testing.T) {
	isolate(t)
	fakeSSH(t, `echo "alice@build.example: Permission denied (publickey)." >&2; exit 255`)
	c, err := New("ssh://alice@build.example")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck // test client
	if err := ping(t, c); err == nil || !strings.Contains(err.Error(), "Permission denied (publickey)") {
		t.Errorf("ping: %v; want ssh's reason", err)
	}
}

func TestSSHHostErrors(t *testing.T) {
	for _, host := range []string{"ssh://", "ssh://alice@", "ssh://host?x=1", "ssh://host#frag"} {
		if _, err := sshDialer(host); err == nil {
			t.Errorf("%s: no error", host)
		}
	}
}
