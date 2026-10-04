// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// A context's endpoint is more than its host. The CLI also keeps the
// context's TLS material in the store (contexts/tls/<digest>/docker/{ca,
// cert,key}.pem) and its SkipTLSVerify flag in the metadata, and it dials
// an ssh:// host by running `docker system dial-stdio` on the far side.
// The SDK does none of that: given only the host, a TLS context is spoken
// to in plain HTTP and an ssh:// host is looked up as a hostname (#33,
// #34). This file is what the CLI does, for the SDK.

// contextTLS is the TLS configuration the CLI builds for a context, or nil
// when the context has neither TLS files nor SkipTLSVerify, which the CLI
// takes to mean plain TCP. A CA file replaces the system roots, as in the
// CLI; with no CA the system roots verify the daemon unless SkipTLSVerify.
func contextTLS(name string, skipVerify bool) (*tls.Config, error) {
	dir := configDir()
	if dir == "" {
		return nil, nil //nolint:nilnil // no TLS is an answer: plain TCP, as the CLI takes it
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", contextDigest(name), "docker")
	read := func(file string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(tlsDir, file)) //nolint:gosec // the user's own docker context store
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	ca, err := read("ca.pem")
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	cert, err := read("cert.pem")
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	key, err := read("key.pem")
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	if ca == nil && cert == nil && key == nil && !skipVerify {
		return nil, nil //nolint:nilnil // no TLS is an answer: plain TCP, as the CLI takes it
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: skipVerify, //nolint:gosec // the context asks for it, as the CLI honors it
	}
	if ca != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("context %q: no certificates in its ca.pem", name)
		}
		cfg.RootCAs = pool
	}
	if cert != nil && key != nil {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("context %q: client certificate: %w", name, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// endpointOpts are the client options that reach host: the host itself,
// the context's TLS when it has some, and for ssh:// a dialer that runs
// `docker system dial-stdio` over ssh.
func endpointOpts(host string, tlsCfg *tls.Config) ([]client.Opt, error) {
	if host == "" {
		return nil, nil
	}
	if strings.HasPrefix(host, "ssh://") {
		dial, err := sshDialer(host)
		if err != nil {
			return nil, err
		}
		// A fresh transport and plain http: ssh is the encryption, and
		// TLS from the environment must not turn the stream into https.
		// The host only names the HTTP requests; the dialer ignores it.
		// WithDialContext must follow WithHost, which sets its own dialer.
		return []client.Opt{
			client.WithHTTPClient(httpClient(nil)),
			client.WithHost("http://docker.example.com"),
			client.WithScheme("http"),
			client.WithDialContext(dial),
		}, nil
	}
	var opts []client.Opt
	if tlsCfg != nil {
		// Before WithHost, which configures this transport for the host.
		opts = append(opts, client.WithHTTPClient(httpClient(tlsCfg)))
	}
	return append(opts, client.WithHost(host)), nil
}

// httpClient is the SDK's default HTTP client, with tlsCfg.
func httpClient(tlsCfg *tls.Config) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
			// The SDK's own limits, so idle connections are released.
			MaxIdleConns:    6,
			IdleConnTimeout: 30 * time.Second,
		},
		CheckRedirect: client.CheckRedirect,
	}
}

// sshDialer parses ssh://[user@]host[:port][/socket] and returns a dialer
// that runs `docker system dial-stdio` there, the CLI's own connection
// helper: one ssh process per connection, its stdin and stdout the conn.
func sshDialer(host string) (func(context.Context, string, string) (net.Conn, error), error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", host, err)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%s names no host", host)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s: an ssh host takes no query or fragment", host)
	}
	args := []string{"-o", "ConnectTimeout=30", "-T"}
	if u.User != nil {
		args = append(args, "-l", u.User.Username())
	}
	if p := u.Port(); p != "" {
		args = append(args, "-p", p)
	}
	// "--" so a hostname can never be read as an ssh option.
	args = append(args, "--", u.Hostname(), "docker")
	if u.Path != "" && u.Path != "/" {
		args = append(args, "--host", "unix://"+u.Path)
	}
	args = append(args, "system", "dial-stdio")
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialCommand(ctx, "ssh", args...)
	}, nil
}

// dialCommand starts name with args and returns its stdin and stdout as a
// connection. The process outlives the dial's context on purpose
// (WithoutCancel): the transport pools the connection, and closing it is
// what ends the process.
func dialCommand(ctx context.Context, name string, args ...string) (net.Conn, error) {
	cmd := exec.CommandContext(
		context.WithoutCancel(ctx),
		name,
		args...) //nolint:gosec // ssh with arguments parsed from the user's own endpoint
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &cmdConn{cmd: cmd, w: stdin, r: stdout}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	return c, nil
}

// cmdConn is a net.Conn over a child process's stdio. Deadlines are not
// supported (as in the CLI's own); requests are bounded by their context.
type cmdConn struct {
	cmd    *exec.Cmd
	w      io.WriteCloser
	r      io.ReadCloser
	stderr limitedBuffer
	once   sync.Once
}

func (c *cmdConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if errors.Is(err, io.EOF) {
		if msg := strings.TrimSpace(c.stderr.String()); msg != "" {
			return n, fmt.Errorf("%s: %s", filepath.Base(c.cmd.Path), msg)
		}
	}
	return n, err
}

func (c *cmdConn) Write(p []byte) (int, error) { return c.w.Write(p) }

// Close ends the process: stdin closed asks it to finish, and a kill
// makes sure a stuck ssh does not outlive the connection.
func (c *cmdConn) Close() error {
	c.once.Do(func() {
		_ = c.w.Close() //nolint:errcheck // the process is being ended either way
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill() //nolint:errcheck // it may already have exited
		}
		_ = c.cmd.Wait() //nolint:errcheck // reaping; a killed process exits non-zero
	})
	return nil
}

func (c *cmdConn) LocalAddr() net.Addr              { return cmdAddr{} }
func (c *cmdConn) RemoteAddr() net.Addr             { return cmdAddr{} }
func (c *cmdConn) SetDeadline(time.Time) error      { return nil }
func (c *cmdConn) SetReadDeadline(time.Time) error  { return nil }
func (c *cmdConn) SetWriteDeadline(time.Time) error { return nil }

type cmdAddr struct{}

func (cmdAddr) Network() string { return "cmd" }
func (cmdAddr) String() string  { return "cmd" }

// limitedBuffer keeps the first 4KiB of a process's stderr, enough for
// ssh's reason ("Permission denied", "Could not resolve hostname").
type limitedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 4096 - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
