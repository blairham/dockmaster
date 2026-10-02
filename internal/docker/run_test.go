package docker

import (
	"strings"
	"testing"

	"github.com/docker/go-connections/nat"
)

func TestRunConfig(t *testing.T) {
	cfg, host, err := RunConfig(RunSpec{
		Image: "nginx:1.27", Ports: []string{"8080:80", "127.0.0.1:9443:443"},
		Env: []string{"A=1", "EMPTY="}, Volumes: []string{"/tmp/site:/usr/share/nginx/html:ro", "data:/data"},
		Command: []string{"nginx", "-g", "daemon off;"}, Remove: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "nginx:1.27" || len(cfg.Env) != 2 || strings.Join(cfg.Cmd, " ") != "nginx -g daemon off;" {
		t.Errorf("config = %+v", cfg)
	}
	if _, ok := cfg.ExposedPorts[nat.Port("80/tcp")]; !ok {
		t.Errorf("80/tcp not exposed: %v", cfg.ExposedPorts)
	}
	if b := host.PortBindings[nat.Port("443/tcp")]; len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "9443" {
		t.Errorf("443 binding = %+v", b)
	}
	if !host.AutoRemove || len(host.Binds) != 2 {
		t.Errorf("host config = %+v", host)
	}

	for spec, want := range map[*RunSpec]string{
		{}:                                        "no image",
		{Image: "x", Ports: []string{"80:http"}}:  "ports",
		{Image: "x", Env: []string{"NOEQUALS"}}:   "KEY=value",
		{Image: "x", Env: []string{"=v"}}:         "KEY=value",
		{Image: "x", Volumes: []string{"/only"}}:  "source:/path",
		{Image: "x", Volumes: []string{":/data"}}: "source:/path",
	} {
		if _, _, err := RunConfig(*spec); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err %v, want one mentioning %q", *spec, err, want)
		}
	}
}
