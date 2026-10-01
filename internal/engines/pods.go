package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Pod is a podman pod: containers sharing a network namespace, managed as
// one. The Docker API has no pods, so these come from the podman CLI.
type Pod struct {
	Created    time.Time
	Machine    string // the podman machine it runs in
	ID         string
	Name       string
	Status     string // Running, Stopped, Exited, Degraded, Created
	Containers []PodContainer
}

// PodContainer is one member of a pod.
type PodContainer struct {
	Name   string
	Status string
}

// Running reports how many of the pod's containers are running.
func (p Pod) Running() int {
	n := 0
	for _, c := range p.Containers {
		if strings.EqualFold(c.Status, "running") {
			n++
		}
	}
	return n
}

// connection is the podman system connection for a machine: its name, or
// name-root when the machine runs rootful — the one its containers live in.
func connection(m Machine) string {
	if m.Rootful {
		return m.Name + "-root"
	}
	return m.Name
}

// podJSON is one entry of `podman pod ps --format json`.
type podJSON struct {
	Created    string `json:"Created"`
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	Status     string `json:"Status"`
	Containers []struct {
		Names  string `json:"Names"`
		Status string `json:"Status"`
	} `json:"Containers"`
}

// Pods lists the pods in every running machine. A machine that fails to
// answer is reported, and the others' pods are still returned.
func (p Podman) Pods(ctx context.Context) ([]Pod, error) {
	machines, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	var (
		pods []Pod
		errs []string
	)
	for _, m := range machines {
		if !m.Running {
			continue
		}
		out, err := p.Run(ctx, "podman", "--connection", connection(m), "pod", "ps", "--format", "json")
		if err != nil {
			errs = append(errs, m.Name+": "+err.Error())
			continue
		}
		var list []podJSON
		if err := json.Unmarshal(out, &list); err != nil {
			errs = append(errs, m.Name+": parsing pod ps: "+err.Error())
			continue
		}
		for _, j := range list {
			pod := Pod{Machine: m.Name, ID: j.ID, Name: j.Name, Status: j.Status}
			if t, err := time.Parse(time.RFC3339Nano, j.Created); err == nil {
				pod.Created = t
			}
			for _, c := range j.Containers {
				pod.Containers = append(pod.Containers, PodContainer{Name: c.Names, Status: c.Status})
			}
			pods = append(pods, pod)
		}
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Machine != pods[j].Machine {
			return pods[i].Machine < pods[j].Machine
		}
		return pods[i].Name < pods[j].Name
	})
	if len(errs) > 0 {
		return pods, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return pods, nil
}

// PodVerb runs a pod lifecycle verb — start, stop, restart, rm — in the
// pod's machine. rm is forced: dockmaster has already asked.
func (p Podman) PodVerb(ctx context.Context, machine, pod, verb string) error {
	m, err := p.machineNamed(ctx, machine)
	if err != nil {
		return err
	}
	args := []string{"--connection", connection(m), "pod", verb}
	if verb == "rm" {
		args = append(args, "-f")
	}
	_, err = p.Run(ctx, "podman", append(args, pod)...)
	return err
}

// PodInspect is `podman pod inspect` for a pod.
func (p Podman) PodInspect(ctx context.Context, machine, pod string) ([]byte, error) {
	m, err := p.machineNamed(ctx, machine)
	if err != nil {
		return nil, err
	}
	return p.Run(ctx, "podman", "--connection", connection(m), "pod", "inspect", pod)
}

func (p Podman) machineNamed(ctx context.Context, name string) (Machine, error) {
	ms, err := p.List(ctx)
	if err != nil {
		return Machine{}, err
	}
	for _, m := range ms {
		if m.Name == name {
			return m, nil
		}
	}
	return Machine{}, fmt.Errorf("no podman machine %s", name)
}
