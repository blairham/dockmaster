package docker

import (
	"testing"
	"time"
)

func TestHumanSize(t *testing.T) {
	// Named fields throughout this file, not positional. `fieldalignment
	// -fix` (part of `make fmt`) reorders struct fields and does NOT rewrite
	// positional literals to match — it silently swapped in/want here once
	// already, turning every case into a type error.
	for _, tc := range []struct {
		want string
		in   int64
	}{
		{in: 0, want: "0B"},
		{in: 999, want: "999B"},
		{in: 1000, want: "1.00kB"},
		{in: 15_500, want: "15.5kB"},
		{in: 150_000, want: "150kB"},
		{in: 1_500_000, want: "1.50MB"},
		{in: 2_400_000_000, want: "2.40GB"},
		{in: -1, want: ""},
	} {
		if got := HumanSize(tc.in); got != tc.want {
			t.Errorf("HumanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseHealth(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{status: "Up 3 hours (healthy)", want: "healthy"},
		{status: "Up 2 minutes (unhealthy)", want: "unhealthy"},
		{status: "Up 5 seconds (health: starting)", want: "starting"},
		{status: "Up 3 hours", want: ""},
		{status: "Exited (0) 2 hours ago", want: ""}, // the (0) is an exit code, not health
		{status: "Up 3 hours (Paused)", want: ""},
		{status: "", want: ""},
	} {
		if got := parseHealth(tc.status); got != tc.want {
			t.Errorf("parseHealth(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestShortID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "sha256:0123456789abcdef0123", want: "0123456789ab"},
		{in: "0123456789abcdef", want: "0123456789ab"},
		{in: "short", want: "short"},
		{in: "", want: ""},
	} {
		if got := shortID(tc.in); got != tc.want {
			t.Errorf("shortID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSince(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		in   time.Time
		want string
	}{
		{in: time.Time{}, want: ""},
		{in: now.Add(-30 * time.Second), want: "30s"},
		{in: now.Add(-5 * time.Minute), want: "5m"},
		{in: now.Add(-3 * time.Hour), want: "3h"},
		{in: now.Add(-50 * time.Hour), want: "2d"},
	} {
		if got := since(tc.in); got != tc.want {
			t.Errorf("since(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProjectsFoldByLabel(t *testing.T) {
	cs := []Container{
		{Name: "a", State: "running", Project: "shop", Service: "web"},
		{Name: "b", State: "running", Project: "shop", Service: "web"}, // same service, scaled
		{Name: "c", State: "exited", Project: "shop", Service: "db"},
		{Name: "d", State: "running", Project: "blog", Service: "app"},
		{Name: "e", State: "running"}, // no project label — must not appear
	}
	got := Projects(cs)
	if len(got) != 2 {
		t.Fatalf("Projects returned %d projects, want 2 (unlabeled containers must not form one)", len(got))
	}
	// Sorted by name: blog, shop.
	if got[0].Name != "blog" || got[1].Name != "shop" {
		t.Fatalf("projects not sorted by name: %v, %v", got[0].Name, got[1].Name)
	}
	shop := got[1]
	if shop.Total != 3 || shop.Running != 2 || shop.Stopped != 1 {
		t.Errorf("shop = %d total / %d running / %d stopped, want 3/2/1", shop.Total, shop.Running, shop.Stopped)
	}
	// A scaled service is one service, not two.
	if len(shop.Services) != 2 {
		t.Errorf("shop services = %v, want 2 distinct (web, db)", shop.Services)
	}
	if shop.Status() != "2/3" {
		t.Errorf("shop status = %q, want 2/3", shop.Status())
	}
}

func TestContextDigestMatchesDockerCLI(t *testing.T) {
	// The docker CLI names each context-store directory with the hex
	// SHA-256 of the context name. This value was taken from a real
	// store; if it ever stops matching, dockyard silently falls back to
	// the default socket and reports a dead daemon on a live machine.
	const wantColima = "f24fd3749c1368328e2b149bec149cb6795619f244c5b584e844961215dadd16"
	if got := contextDigest("colima"); got != wantColima {
		t.Errorf("contextDigest(colima) = %s, want %s", got, wantColima)
	}
}

func TestCleanBuildStep(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "/bin/sh -c #(nop)  CMD [\"nginx\"]", want: "CMD [\"nginx\"]"},
		{in: "/bin/sh -c apt-get update  &&  apt-get install -y curl", want: "apt-get update && apt-get install -y curl"},
		{in: "COPY dir:abc in /app ", want: "COPY dir:abc in /app"},
	} {
		if got := cleanBuildStep(tc.in); got != tc.want {
			t.Errorf("cleanBuildStep(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKeepRealTags(t *testing.T) {
	got := keepRealTags([]string{"nginx:latest", "<none>:<none>", "", "acme/api:v2"})
	if len(got) != 2 || got[0] != "nginx:latest" || got[1] != "acme/api:v2" {
		t.Errorf("keepRealTags = %v, want the two real tags only", got)
	}
}

func TestIndentJSONSortsKeys(t *testing.T) {
	// The daemon's field order is not stable between calls; an unsorted
	// re-render would make the inspect viewport jump on every refresh.
	out, err := indentJSON([]byte(`{"zeta":1,"alpha":2}`))
	if err != nil {
		t.Fatalf("indentJSON: %v", err)
	}
	s := string(out)
	if idxAlpha, idxZeta := indexOf(s, "alpha"), indexOf(s, "zeta"); idxAlpha > idxZeta {
		t.Errorf("keys not sorted:\n%s", s)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFormatPortsPrefersPublished(t *testing.T) {
	// When anything is published, the merely-exposed ports are noise in a
	// narrow column and are dropped.
	ports := []portFixture{{Private: 80, Public: 8080, Type: "tcp"}, {Private: 443, Type: "tcp"}}
	if got := formatPorts(toPorts(ports)); got != "8080→80/tcp" {
		t.Errorf("formatPorts = %q, want only the published mapping", got)
	}
	// With nothing published, the exposed set is all there is to show.
	ports = []portFixture{{Private: 443, Type: "tcp"}, {Private: 80, Type: "tcp"}}
	if got := formatPorts(toPorts(ports)); got != "443/tcp 80/tcp" {
		t.Errorf("formatPorts = %q, want the exposed ports", got)
	}
}
