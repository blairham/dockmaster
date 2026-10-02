package kind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRunsKindAgainstTheMachine(t *testing.T) {
	var gotEnv []string
	var gotStdin string
	var gotArgs []string
	run := func(_ context.Context, env []string, stdin string, args ...string) ([]byte, error) {
		gotEnv, gotStdin, gotArgs = env, stdin, args
		return nil, nil
	}
	if err := Create(context.Background(), run, "unix:///h/.colima/default/docker.sock", "k8s"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotEnv, " ") != "DOCKER_HOST=unix:///h/.colima/default/docker.sock" {
		t.Errorf("env %v: kind must target the machine's daemon", gotEnv)
	}
	if strings.Join(gotArgs, " ") != "create cluster --name k8s --config -" || gotStdin != Config {
		t.Errorf("args %v", gotArgs)
	}
	for _, want := range []string{"- role: control-plane", "- role: worker", `config_path = "/etc/containerd/certs.d"`} {
		if !strings.Contains(Config, want) {
			t.Errorf("config lacks %q", want)
		}
	}
}

func TestNameNeverReusesAnotherMachinesContext(t *testing.T) {
	if got := Name([]string{"colima", "trading-platform-dev"}, "orbstack"); got != "k8s" {
		t.Errorf("no kind-k8s yet: %q", got)
	}
	if got := Name([]string{"kind-k8s", "colima"}, "orbstack"); got != "k8s-orbstack" {
		t.Errorf("kind-k8s taken: %q", got)
	}
}

func TestContextsReadsKubeconfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(
		p,
		[]byte("apiVersion: v1\ncontexts:\n- name: kind-k8s\n  context: {cluster: kind-k8s}\n- name: colima\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", p+string(os.PathListSeparator)+"/elsewhere")
	got, err := Contexts()
	if err != nil || strings.Join(got, ",") != "kind-k8s,colima" {
		t.Errorf("Contexts = %v, %v", got, err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "missing"))
	if got, err := Contexts(); err != nil || len(got) != 0 {
		t.Errorf("missing kubeconfig: %v, %v", got, err)
	}
}
