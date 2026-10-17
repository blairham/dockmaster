// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// LabelStack is the label `docker stack deploy` stamps on every service,
// network, secret and config of a stack — the only record of the stack,
// as the compose labels are of a project.
const LabelStack = "com.docker.stack.namespace"

// The service modes, as `docker service ls` prints them.
const (
	ModeReplicated    = "replicated"
	ModeGlobal        = "global"
	ModeReplicatedJob = "replicated-job"
	ModeGlobalJob     = "global-job"
)

// ErrNotScalable is a scale asked of a service whose mode has no replica
// count: global services run one task per node, and jobs run to a total.
var ErrNotScalable = errors.New("only a replicated service can be scaled")

// Service is one row of the services view.
type Service struct {
	Created time.Time
	Updated time.Time
	Labels  map[string]string

	ID    string
	Name  string
	Image string
	Mode  string
	Stack string
	Ports string
	// Update is the rolling update's state ("updating", "paused",
	// "rollback_completed", …), "" when none ever ran.
	Update string

	// Running and Desired are the service's task counts, from the list's
	// status block.
	Running uint64
	Desired uint64
}

// Short is the 12-char service ID.
func (s Service) Short() string { return shortID(s.ID) }

// Age is the service's creation age, one unit.
func (s Service) Age() string { return since(s.Created) }

// Replicas is RUNNING/DESIRED, as `docker service ls` prints it.
func (s Service) Replicas() string { return fmt.Sprintf("%d/%d", s.Running, s.Desired) }

// Scalable reports whether the service has a replica count to change.
func (s Service) Scalable() bool { return s.Mode == ModeReplicated }

// serviceMode names a service's mode.
func serviceMode(m swarm.ServiceMode) string {
	switch {
	case m.Global != nil:
		return ModeGlobal
	case m.ReplicatedJob != nil:
		return ModeReplicatedJob
	case m.GlobalJob != nil:
		return ModeGlobalJob
	}
	return ModeReplicated
}

// serviceImage is the image a service runs, without the digest the daemon
// pins it to at create: `nginx:1.27`, not `nginx:1.27@sha256:…`.
func serviceImage(spec swarm.ServiceSpec) string {
	if spec.TaskTemplate.ContainerSpec == nil {
		return ""
	}
	img, _, _ := strings.Cut(spec.TaskTemplate.ContainerSpec.Image, "@")
	return img
}

// servicePorts renders published ports as `docker service ls` does:
// *:8080->80/tcp, with host-mode ports marked.
func servicePorts(ports []swarm.PortConfig) string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.PublishedPort == 0 {
			continue
		}
		s := fmt.Sprintf("*:%d->%d/%s", p.PublishedPort, p.TargetPort, p.Protocol)
		if p.PublishMode == swarm.PortConfigPublishModeHost {
			s += " (host)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// Services lists the swarm's services with their task counts.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	res, err := c.api.ServiceList(ctx, client.ServiceListOptions{Status: true})
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	out := make([]Service, 0, len(res.Items))
	for _, s := range res.Items {
		svc := Service{
			ID:      s.ID,
			Name:    s.Spec.Name,
			Image:   serviceImage(s.Spec),
			Mode:    serviceMode(s.Spec.Mode),
			Labels:  s.Spec.Labels,
			Stack:   s.Spec.Labels[LabelStack],
			Ports:   servicePorts(s.Endpoint.Ports),
			Created: s.CreatedAt,
			Updated: s.UpdatedAt,
		}
		if s.ServiceStatus != nil {
			svc.Running, svc.Desired = s.ServiceStatus.RunningTasks, s.ServiceStatus.DesiredTasks
		}
		if s.UpdateStatus != nil {
			svc.Update = string(s.UpdateStatus.State)
		}
		out = append(out, svc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InspectService returns pretty-printed inspect JSON for a service.
func (c *Client) InspectService(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting service %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// updateService reads the service's current spec and version, lets change
// edit the spec, and writes it back — the read-modify-write the API
// requires, since an update carries the version it was based on and the
// manager refuses one that is out of date.
func (c *Client) updateService(ctx context.Context, id string, change func(*swarm.Service) error) error {
	res, err := c.api.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspecting service %s: %w", shortID(id), err)
	}
	svc := res.Service
	if changeErr := change(&svc); changeErr != nil {
		return changeErr
	}
	_, err = c.api.ServiceUpdate(ctx, svc.ID, client.ServiceUpdateOptions{Version: svc.Version, Spec: svc.Spec})
	if err != nil {
		return fmt.Errorf("updating service %s: %w", svc.Spec.Name, err)
	}
	return nil
}

// ScaleService sets a replicated service's replica count, as `docker
// service scale name=n`.
func (c *Client) ScaleService(ctx context.Context, id string, replicas uint64) error {
	return c.updateService(ctx, id, func(s *swarm.Service) error {
		if s.Spec.Mode.Replicated == nil {
			return fmt.Errorf("%s is a %s service: %w", s.Spec.Name, serviceMode(s.Spec.Mode), ErrNotScalable)
		}
		s.Spec.Mode.Replicated.Replicas = &replicas
		return nil
	})
}

// RestartService replaces every task of a service under its update
// policy, as `docker service update --force`: the spec is unchanged but
// for the counter the manager reads as "start over".
func (c *Client) RestartService(ctx context.Context, id string) error {
	return c.updateService(ctx, id, func(s *swarm.Service) error {
		s.Spec.TaskTemplate.ForceUpdate++
		return nil
	})
}

// RemoveService deletes a service; the manager stops its tasks.
func (c *Client) RemoveService(ctx context.Context, id string) error {
	if _, err := c.api.ServiceRemove(ctx, id, client.ServiceRemoveOptions{}); err != nil {
		return fmt.Errorf("removing service %s: %w", shortID(id), err)
	}
	return nil
}

// Task is one row of the tasks view: one slot of a service on one node.
type Task struct {
	Updated time.Time

	ID        string
	Name      string
	ServiceID string
	Service   string
	NodeID    string
	Node      string
	Image     string
	Desired   string
	State     string
	// Err is why the task failed or was rejected, "" when it did not.
	Err         string
	ContainerID string

	Slot     int
	ExitCode int
}

// Short is the 12-char task ID.
func (t Task) Short() string { return shortID(t.ID) }

// Age is how long ago the task reached its current state.
func (t Task) Age() string { return since(t.Updated) }

// Current is the task's state and how long it has been in it, as `docker
// service ps` prints "Running 3 hours ago".
func (t Task) Current() string { return t.State + " " + t.Age() }

// Live reports whether the task is meant to be running: the manager's
// desired state is still running (or ready, for one waiting on its turn),
// not a shutdown of an old task kept as history.
func (t Task) Live() bool {
	return t.Desired == string(swarm.TaskStateRunning) || t.Desired == string(swarm.TaskStateReady) ||
		t.Desired == string(swarm.TaskStateAccepted)
}

// TaskScope narrows a task listing to one service or one node; the zero
// value lists every task.
type TaskScope struct {
	Service string
	Node    string
}

// Tasks lists the swarm's tasks, newest first within each slot, named as
// `docker service ps` names them: service.slot, or service.node for a
// global service. Service and node names take a listing of each.
func (c *Client) Tasks(ctx context.Context, scope TaskScope) ([]Task, error) {
	f := make(client.Filters)
	if scope.Service != "" {
		f = f.Add("service", scope.Service)
	}
	if scope.Node != "" {
		f = f.Add("node", scope.Node)
	}
	res, err := c.api.TaskList(ctx, client.TaskListOptions{Filters: f})
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	services, err := c.api.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	svcNames := make(map[string]string, len(services.Items))
	for _, s := range services.Items {
		svcNames[s.ID] = s.Spec.Name
	}
	nodes, err := c.api.NodeList(ctx, client.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	nodeNames := make(map[string]string, len(nodes.Items))
	for _, n := range nodes.Items {
		nodeNames[n.ID] = n.Description.Hostname
	}
	return taskRows(res.Items, svcNames, nodeNames), nil
}

// taskRows turns API tasks into rows, sorted by name then newest first, so
// each slot reads as `docker service ps` shows it: the live task, then its
// history.
func taskRows(items []swarm.Task, svcNames, nodeNames map[string]string) []Task {
	out := make([]Task, 0, len(items))
	for _, t := range items {
		svc := svcNames[t.ServiceID]
		if svc == "" {
			svc = shortID(t.ServiceID)
		}
		name := svc + "." + strconv.Itoa(t.Slot)
		if t.Slot == 0 {
			name = svc + "." + t.NodeID
		}
		row := Task{
			ID:        t.ID,
			Name:      name,
			ServiceID: t.ServiceID,
			Service:   svc,
			NodeID:    t.NodeID,
			Node:      nodeNames[t.NodeID],
			Image:     serviceImage(swarm.ServiceSpec{TaskTemplate: t.Spec}),
			Desired:   string(t.DesiredState),
			State:     string(t.Status.State),
			Err:       t.Status.Err,
			Slot:      t.Slot,
			Updated:   t.Status.Timestamp,
		}
		if cs := t.Status.ContainerStatus; cs != nil {
			row.ContainerID, row.ExitCode = cs.ContainerID, cs.ExitCode
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

// InspectTask returns pretty-printed inspect JSON for a task.
func (c *Client) InspectTask(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.TaskInspect(ctx, id, client.TaskInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting task %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// SwarmNode is one row of the swarm nodes view.
type SwarmNode struct {
	Created time.Time
	Labels  map[string]string

	ID           string
	Hostname     string
	Role         string
	Availability string
	State        string
	// Manager is the manager status column of `docker node ls`: Leader,
	// Reachable or Unreachable; "" on a worker.
	Manager string
	Engine  string
	Addr    string

	// Self is the node the daemon on screen is.
	Self bool
}

// Short is the 12-char node ID.
func (n SwarmNode) Short() string { return shortID(n.ID) }

// Age is the node's join age, one unit.
func (n SwarmNode) Age() string { return since(n.Created) }

// managerStatus is the MANAGER STATUS column for a node.
func managerStatus(m *swarm.ManagerStatus) string {
	switch {
	case m == nil:
		return ""
	case m.Leader:
		return "Leader"
	case m.Reachability == swarm.ReachabilityReachable:
		return "Reachable"
	case m.Reachability == swarm.ReachabilityUnreachable:
		return "Unreachable"
	}
	return string(m.Reachability)
}

// SwarmNodes lists the swarm's nodes, by hostname.
func (c *Client) SwarmNodes(ctx context.Context) ([]SwarmNode, error) {
	res, err := c.api.NodeList(ctx, client.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	out := make([]SwarmNode, 0, len(res.Items))
	for _, n := range res.Items {
		out = append(out, SwarmNode{
			ID:           n.ID,
			Hostname:     n.Description.Hostname,
			Role:         string(n.Spec.Role),
			Availability: string(n.Spec.Availability),
			State:        string(n.Status.State),
			Manager:      managerStatus(n.ManagerStatus),
			Engine:       n.Description.Engine.EngineVersion,
			Addr:         n.Status.Addr,
			Labels:       n.Spec.Labels,
			Created:      n.CreatedAt,
			Self:         n.ID == c.swarmNodeID(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out, nil
}

// swarmNodeID is the node the daemon is, safe on a nil client.
func (c *Client) swarmNodeID() string {
	if c == nil {
		return ""
	}
	return c.SwarmNodeID
}

// InspectSwarmNode returns pretty-printed inspect JSON for a swarm node.
func (c *Client) InspectSwarmNode(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.NodeInspect(ctx, id, client.NodeInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting node %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// ParseAvailability reads a node availability: active, pause or drain.
func ParseAvailability(s string) (swarm.NodeAvailability, error) {
	switch a := swarm.NodeAvailability(s); a {
	case swarm.NodeAvailabilityActive, swarm.NodeAvailabilityPause, swarm.NodeAvailabilityDrain:
		return a, nil
	}
	return "", fmt.Errorf("%q is not a node availability (active, pause or drain)", s)
}

// SetNodeAvailability is `docker node update --availability`: active
// schedules tasks on the node, pause stops new ones landing there, and
// drain moves its tasks elsewhere.
func (c *Client) SetNodeAvailability(ctx context.Context, id, availability string) error {
	avail, err := ParseAvailability(availability)
	if err != nil {
		return err
	}
	res, err := c.api.NodeInspect(ctx, id, client.NodeInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspecting node %s: %w", shortID(id), err)
	}
	n := res.Node
	n.Spec.Availability = avail
	if _, err := c.api.NodeUpdate(ctx, n.ID, client.NodeUpdateOptions{Version: n.Version, Spec: n.Spec}); err != nil {
		return fmt.Errorf("setting node %s to %s: %w", n.Description.Hostname, avail, err)
	}
	return nil
}

// RemoveSwarmNode is `docker node rm` without --force: the manager
// refuses a node that is still up, and says so.
func (c *Client) RemoveSwarmNode(ctx context.Context, id string) error {
	if _, err := c.api.NodeRemove(ctx, id, client.NodeRemoveOptions{}); err != nil {
		return fmt.Errorf("removing node %s: %w", shortID(id), err)
	}
	return nil
}

// SwarmData is one row of the secrets or configs view. Only the metadata:
// a secret's value never leaves the manager, and a config's is in its
// inspect.
type SwarmData struct {
	Created time.Time
	Updated time.Time
	Labels  map[string]string

	ID     string
	Name   string
	Stack  string
	Driver string
}

// Short is the 12-char ID.
func (d SwarmData) Short() string { return shortID(d.ID) }

// Age is the creation age, one unit.
func (d SwarmData) Age() string { return since(d.Created) }

// sortData orders secrets or configs by name.
func sortData(out []SwarmData) []SwarmData {
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Secrets lists the swarm's secrets.
func (c *Client) Secrets(ctx context.Context) ([]SwarmData, error) {
	res, err := c.api.SecretList(ctx, client.SecretListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	out := make([]SwarmData, 0, len(res.Items))
	for _, s := range res.Items {
		d := SwarmData{
			ID: s.ID, Name: s.Spec.Name, Labels: s.Spec.Labels, Stack: s.Spec.Labels[LabelStack],
			Created: s.CreatedAt, Updated: s.UpdatedAt,
		}
		if s.Spec.Driver != nil {
			d.Driver = s.Spec.Driver.Name
		}
		out = append(out, d)
	}
	return sortData(out), nil
}

// InspectSecret returns pretty-printed inspect JSON for a secret, which
// the manager sends without the value.
func (c *Client) InspectSecret(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.SecretInspect(ctx, id, client.SecretInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting secret %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// RemoveSecret deletes a secret; the manager refuses one a service uses.
func (c *Client) RemoveSecret(ctx context.Context, id string) error {
	if _, err := c.api.SecretRemove(ctx, id, client.SecretRemoveOptions{}); err != nil {
		return fmt.Errorf("removing secret %s: %w", shortID(id), err)
	}
	return nil
}

// Configs lists the swarm's configs.
func (c *Client) Configs(ctx context.Context) ([]SwarmData, error) {
	res, err := c.api.ConfigList(ctx, client.ConfigListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing configs: %w", err)
	}
	out := make([]SwarmData, 0, len(res.Items))
	for _, s := range res.Items {
		d := SwarmData{
			ID: s.ID, Name: s.Spec.Name, Labels: s.Spec.Labels, Stack: s.Spec.Labels[LabelStack],
			Created: s.CreatedAt, Updated: s.UpdatedAt,
		}
		if s.Spec.Templating != nil {
			d.Driver = s.Spec.Templating.Name
		}
		out = append(out, d)
	}
	return sortData(out), nil
}

// InspectConfig returns pretty-printed inspect JSON for a config, its
// data base64 as the API carries it.
func (c *Client) InspectConfig(ctx context.Context, id string) ([]byte, error) {
	res, err := c.api.ConfigInspect(ctx, id, client.ConfigInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspecting config %s: %w", shortID(id), err)
	}
	return indentJSON(res.Raw)
}

// RemoveConfig deletes a config; the manager refuses one a service uses.
func (c *Client) RemoveConfig(ctx context.Context, id string) error {
	if _, err := c.api.ConfigRemove(ctx, id, client.ConfigRemoveOptions{}); err != nil {
		return fmt.Errorf("removing config %s: %w", shortID(id), err)
	}
	return nil
}

// The swarm log sources: a service's tasks merged, or one task's.
const (
	SwarmLogService = "service"
	SwarmLogTask    = "task"
)

// StreamSwarmLogs opens a follow-mode tail of a service's or a task's
// output, as `docker service logs -f`. The daemon frames it as it does a
// non-TTY container's unless the service runs with a TTY.
func (c *Client) StreamSwarmLogs(
	ctx context.Context, kind, id string, tail int, timestamps bool, since time.Time,
) (*LogStream, error) {
	o := logOptions(tail, timestamps, since)
	var (
		rc   io.ReadCloser
		spec *swarm.ContainerSpec
		err  error
	)
	switch kind {
	case SwarmLogService:
		var insp client.ServiceInspectResult
		if insp, err = c.api.ServiceInspect(ctx, id, client.ServiceInspectOptions{}); err != nil {
			return nil, fmt.Errorf("inspecting service %s for log stream: %w", shortID(id), err)
		}
		spec = insp.Service.Spec.TaskTemplate.ContainerSpec
		rc, err = c.api.ServiceLogs(ctx, id, client.ServiceLogsOptions{
			ShowStdout: true, ShowStderr: true, Follow: true,
			Timestamps: o.Timestamps, Tail: o.Tail, Since: o.Since,
		})
	case SwarmLogTask:
		var insp client.TaskInspectResult
		if insp, err = c.api.TaskInspect(ctx, id, client.TaskInspectOptions{}); err != nil {
			return nil, fmt.Errorf("inspecting task %s for log stream: %w", shortID(id), err)
		}
		spec = insp.Task.Spec.ContainerSpec
		rc, err = c.api.TaskLogs(ctx, id, client.TaskLogsOptions{
			ShowStdout: true, ShowStderr: true, Follow: true,
			Timestamps: o.Timestamps, Tail: o.Tail, Since: o.Since,
		})
	default:
		return nil, fmt.Errorf("no %q swarm log source", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("streaming logs for %s %s: %w", kind, shortID(id), err)
	}
	return demuxLogs(ctx, rc, spec != nil && spec.TTY), nil
}
