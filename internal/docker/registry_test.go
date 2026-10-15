// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// isolateDockerConfig points DOCKER_CONFIG at a fresh directory, PATH at
// one that holds only the system tools and a fake credential helper, and
// turns the native-store default off — so no test here can read or write
// the user's docker config or reach a real keychain. It returns the
// config directory and the fake helper's state directory.
func isolateDockerConfig(t *testing.T) (cfgDir, helperDir string) {
	t.Helper()
	cfgDir = t.TempDir()
	helperDir = t.TempDir()
	bin := t.TempDir()
	t.Setenv("DOCKER_CONFIG", cfgDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FAKE_HELPER_DIR", helperDir)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	old := DefaultCredsStore
	DefaultCredsStore = func() string { return "" }
	t.Cleanup(func() { DefaultCredsStore = old })

	// The fake helper speaks the credential-helper protocol from files:
	// get answers $FAKE_HELPER_DIR/get.json when the asked server is in
	// $FAKE_HELPER_DIR/known, store and erase record their stdin.
	script := `#!/bin/sh
d="$FAKE_HELPER_DIR"
in=$(cat)
case "$1" in
get)
	if [ -f "$d/known" ] && grep -qxF "$in" "$d/known"; then cat "$d/get.json"; exit 0; fi
	echo "credentials not found in native keychain"; exit 1 ;;
store) printf '%s' "$in" > "$d/stored" ;;
erase) printf '%s' "$in" > "$d/erased" ;;
*) echo "unknown verb $1" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(
		filepath.Join(bin, "docker-credential-fake"),
		[]byte(script),
		0o700,
	); err != nil { //nolint:gosec // a test helper must be executable
		t.Fatal(err)
	}
	return cfgDir, helperDir
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readConfig(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func TestNormalizeRegistry(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{in: "", want: DockerHubServer},
		{in: "docker.io", want: DockerHubServer},
		{in: "https://index.docker.io/v1/", want: DockerHubServer},
		{in: "registry-1.docker.io", want: DockerHubServer},
		{in: "ghcr.io", want: "ghcr.io"},
		{in: "https://GHCR.io/", want: "ghcr.io"},
		{in: "localhost:5001", want: "localhost:5001"},
	}
	for _, c := range cases {
		if got := NormalizeRegistry(c.in); got != c.want {
			t.Errorf("NormalizeRegistry(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRegistryForRef(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{in: "nginx", want: DockerHubServer},
		{in: "library/nginx:1", want: DockerHubServer},
		{in: "blair/app:v1", want: DockerHubServer},
		{in: "ghcr.io/blair/app:v1", want: "ghcr.io"},
		{in: "localhost:5001/app", want: "localhost:5001"},
		{in: "localhost/app", want: "localhost"},
	}
	for _, c := range cases {
		if got := RegistryForRef(c.in); got != c.want {
			t.Errorf("RegistryForRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRegistriesListsConfigAndHelpers: every auths and credHelpers entry
// is a row, a file credential resolves with its user, a helper is asked,
// and a helper with nothing stored is "not logged in", not an error.
func TestRegistriesListsConfigAndHelpers(t *testing.T) {
	cfg, helper := isolateDockerConfig(t)
	writeConfig(t, cfg, `{
		"auths": {
			"https://index.docker.io/v1/": {"auth": "`+basicAuth("blair", "s3cret")+`"},
			"quay.io": {}
		},
		"credHelpers": {"ghcr.io": "fake", "quay.io": "fake"},
		"currentContext": "colima"
	}`)
	if err := os.WriteFile(filepath.Join(helper, "known"), []byte("ghcr.io\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helper, "get.json"),
		[]byte(`{"ServerURL":"ghcr.io","Username":"octo","Secret":"pat"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Registries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []RegistryAuth{
		{Server: "ghcr.io", Helper: "fake", Username: "octo", LoggedIn: true},
		{Server: DockerHubServer, Username: "blair", LoggedIn: true},
		{Server: "quay.io", Helper: "fake"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows %+v, want %+v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRegistriesNeverCarryASecret: no field of a listed row holds the
// password, whatever store it came from.
func TestRegistriesNeverCarryASecret(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"auths": {"ghcr.io": {"auth": "`+basicAuth("octo", "hunter2")+`"}}}`)
	got, err := Registries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "hunter2") { //nolint:errchkjson // plain struct
		t.Errorf("a listed row carries the secret: %s", b)
	}
}

// TestRegistriesReportsAMissingHelper: a helper the config names that is
// not installed is a row with the reason, not a failed listing.
func TestRegistriesReportsAMissingHelper(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"auths": {"ghcr.io": {}}, "credsStore": "nosuch"}`)
	got, err := Registries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].LoggedIn || !strings.Contains(got[0].Err, "docker-credential-nosuch") {
		t.Errorf("got %+v, want one row naming the missing helper", got)
	}
}

// TestRegistriesWithNoConfig: no config.json is no registries, not an
// error — a fresh machine.
func TestRegistriesWithNoConfig(t *testing.T) {
	isolateDockerConfig(t)
	got, err := Registries(context.Background())
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want nothing, no error", got, err)
	}
}

// TestStoreCredentialInConfigKeepsOtherKeys: with no helper the
// credential goes into auths as base64 user:password, and every other
// key of config.json — contexts, proxies — survives the write.
func TestStoreCredentialInConfigKeepsOtherKeys(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"currentContext": "colima", "proxies": {"default": {"httpProxy": "x"}}}`)
	if err := StoreCredential(context.Background(), Credential{
		Server: "ghcr.io", Username: "octo", Password: "pat",
	}); err != nil {
		t.Fatal(err)
	}
	m := readConfig(t, cfg)
	if m["currentContext"] != "colima" || m["proxies"] == nil {
		t.Errorf("other keys lost: %v", m)
	}
	auths, _ := m["auths"].(map[string]any)
	entry, _ := auths["ghcr.io"].(map[string]any)
	if entry["auth"] != basicAuth("octo", "pat") {
		t.Errorf("auths[ghcr.io] = %v", entry)
	}
	st, err := os.Stat(filepath.Join(cfg, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("config.json mode %v, want 0600", st.Mode().Perm())
	}
	cred, ok, err := ResolveCredential(context.Background(), "https://ghcr.io")
	if err != nil || !ok || cred.Username != "octo" || cred.Password != "pat" {
		t.Errorf("ResolveCredential = %+v %v %v", cred, ok, err)
	}
}

// TestStoreCredentialThroughHelper: with a credsStore the secret goes to
// the helper on stdin and config.json gets only an empty marker entry.
func TestStoreCredentialThroughHelper(t *testing.T) {
	cfg, helper := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"credsStore": "fake"}`)
	if err := StoreCredential(context.Background(), Credential{
		Server: DockerHubServer, Username: "blair", Password: "s3cret",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(helper, "stored"))
	if err != nil {
		t.Fatal(err)
	}
	var hc helperCredential
	if err = json.Unmarshal(stored, &hc); err != nil {
		t.Fatal(err)
	}
	if hc != (helperCredential{ServerURL: DockerHubServer, Username: "blair", Secret: "s3cret"}) {
		t.Errorf("helper got %+v", hc)
	}
	b, err := os.ReadFile(filepath.Join(cfg, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), basicAuth("blair", "s3cret")) {
		t.Errorf("config.json holds the secret: %s", b)
	}
	if !strings.Contains(string(b), DockerHubServer) {
		t.Errorf("config.json does not list the registry: %s", b)
	}
}

// TestStoreCredentialAdoptsTheNativeStore: on a config with no store, the
// platform's helper is used and recorded as credsStore, as `docker login`
// does — the password never lands in plain text.
func TestStoreCredentialAdoptsTheNativeStore(t *testing.T) {
	cfg, helper := isolateDockerConfig(t)
	DefaultCredsStore = func() string { return "fake" }
	if err := StoreCredential(context.Background(), Credential{
		Server: "ghcr.io", Username: "octo", Password: "pat",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(helper, "stored")); err != nil {
		t.Errorf("the helper was not asked to store: %v", err)
	}
	if m := readConfig(t, cfg); m["credsStore"] != "fake" {
		t.Errorf("credsStore = %v, want fake", m["credsStore"])
	}
}

// TestStoreCredentialKeepsAnIdentityToken: a token the registry handed
// back is stored instead of the password, under the CLI's "<token>" user.
func TestStoreCredentialKeepsAnIdentityToken(t *testing.T) {
	_, helper := isolateDockerConfig(t)
	DefaultCredsStore = func() string { return "fake" }
	if err := StoreCredential(context.Background(), Credential{
		Server: "ghcr.io", Username: "octo", Password: "pat", IdentityToken: "tok",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(helper, "stored"))
	if err != nil {
		t.Fatal(err)
	}
	var hc helperCredential
	if err = json.Unmarshal(stored, &hc); err != nil {
		t.Fatal(err)
	}
	if hc != (helperCredential{ServerURL: "ghcr.io", Username: "<token>", Secret: "tok"}) {
		t.Errorf("helper got %+v", hc)
	}
}

// TestLogoutErasesAndDropsTheEntry: logout asks the helper to erase and
// removes the auths entry; the other registries stay.
func TestLogoutErasesAndDropsTheEntry(t *testing.T) {
	cfg, helper := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"auths": {"ghcr.io": {}, "quay.io": {"auth": "`+basicAuth("a", "b")+`"}},
		"credHelpers": {"ghcr.io": "fake"}}`)
	if err := Logout(context.Background(), "ghcr.io"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(helper, "erased")); err != nil || string(b) != "ghcr.io" {
		t.Errorf("helper erase got %q, %v", b, err)
	}
	auths, _ := readConfig(t, cfg)["auths"].(map[string]any)
	if _, ok := auths["ghcr.io"]; ok {
		t.Errorf("ghcr.io still in auths: %v", auths)
	}
	if _, ok := auths["quay.io"]; !ok {
		t.Errorf("quay.io was dropped too: %v", auths)
	}
}

// TestLogoutMatchesByHostname: `https://ghcr.io` in the config is found
// from `ghcr.io`, the way the CLI matches.
func TestLogoutMatchesByHostname(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	writeConfig(t, cfg, `{"auths": {"https://ghcr.io": {"auth": "`+basicAuth("a", "b")+`"}}}`)
	if err := Logout(context.Background(), "ghcr.io"); err != nil {
		t.Fatal(err)
	}
	if auths, _ := readConfig(t, cfg)["auths"].(map[string]any); len(auths) != 0 {
		t.Errorf("auths left: %v", auths)
	}
}

// TestStoreCredentialFollowsASymlinkedConfig: a config.json that is a
// link (a dotfiles manager's) stays a link; the target is rewritten.
func TestStoreCredentialFollowsASymlinkedConfig(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	target := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cfg, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredential(
		context.Background(),
		Credential{Server: "ghcr.io", Username: "a", Password: "b"},
	); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(cfg, "config.json")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("config.json is no longer a link: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "ghcr.io") { //nolint:gosec // test temp path
		t.Errorf("the link's target was not written: %s", b)
	}
}

// TestLoginValidatesThenStores: the daemon's /auth is asked first, and
// only an accepted login is stored; a refused one stores nothing.
func TestLoginValidatesThenStores(t *testing.T) {
	cfg, _ := isolateDockerConfig(t)
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		switch version.ReplaceAllString(r.URL.Path, "") {
		case "/_ping":
			_, _ = w.Write([]byte("OK"))
		case "/auth":
			_ = json.NewDecoder(r.Body).Decode(&got)
			if got["password"] != "right" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized: incorrect username or password"}`))
				return
			}
			_, _ = w.Write([]byte(`{"Status":"Login Succeeded"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	if _, lerr := c.Login(context.Background(), "ghcr.io", "octo", "wrong"); lerr == nil {
		t.Fatal("a refused login returned no error")
	}
	if _, serr := os.Stat(filepath.Join(cfg, "config.json")); serr == nil {
		t.Error("a refused login wrote config.json")
	}

	key, err := c.Login(context.Background(), "https://ghcr.io/", "octo", "right")
	if err != nil {
		t.Fatal(err)
	}
	if key != "ghcr.io" || got["serveraddress"] != "ghcr.io" || got["username"] != "octo" {
		t.Errorf("key %q, daemon got %v", key, got)
	}
	if cred, ok, err := ResolveCredential(context.Background(), "ghcr.io"); err != nil || !ok || cred.Password != "right" {
		t.Errorf("stored credential = %+v %v %v", cred, ok, err)
	}
}

func TestCredentialAuthHeader(t *testing.T) {
	h, err := Credential{Server: "ghcr.io", Username: "octo", Password: "pat"}.AuthHeader()
	if err != nil {
		t.Fatal(err)
	}
	b, err := base64.URLEncoding.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["username"] != "octo" || m["password"] != "pat" || m["serveraddress"] != "ghcr.io" {
		t.Errorf("decoded header %v", m)
	}
}
