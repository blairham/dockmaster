package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// indentJSON re-indents a raw inspect payload. The daemon returns it
// minified; the inspect view is a read-only viewport, so the only thing
// that makes it legible is indentation.
func indentJSON(raw []byte) ([]byte, error) {
	// A raw inspect body is a single object. Unmarshal-then-marshal (rather
	// than json.Indent) so map keys come back sorted — the daemon's field
	// order is not stable between calls, and an unsorted re-render makes
	// the viewport jump on every 2-second refresh.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		var buf bytes.Buffer
		if ierr := json.Indent(&buf, raw, "", "  "); ierr != nil {
			return nil, fmt.Errorf("formatting inspect output: %w", err)
		}
		return buf.Bytes(), nil
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("formatting inspect output: %w", err)
	}
	return out, nil
}

// HumanSize renders a byte count the way docker does: three significant
// digits, SI units, no trailing zeros.
func HumanSize(b int64) string {
	if b < 0 {
		return ""
	}
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	val := float64(b) / float64(div)
	units := [...]string{"kB", "MB", "GB", "TB", "PB"}
	switch {
	case val >= 100:
		return fmt.Sprintf("%.0f%s", val, units[exp])
	case val >= 10:
		return fmt.Sprintf("%.1f%s", val, units[exp])
	default:
		return fmt.Sprintf("%.2f%s", val, units[exp])
	}
}

// Project is one row of the compose-projects view: an aggregate over every
// container carrying the same com.docker.compose.project label.
type Project struct {
	Name       string
	Age        string
	WorkingDir string
	Services   []string
	// ConfigFiles and EnvFiles are what `docker compose up` was given, read
	// back from the labels. Empty when the containers predate the labels
	// or were not made by the Compose CLI.
	ConfigFiles []string
	EnvFiles    []string
	Total       int
	Running     int
	Stopped     int
}

// Projects folds a container list into compose projects. Containers with no
// project label are gathered under the empty name and dropped — an
// unlabeled container is not part of any project, and inventing a
// "(standalone)" bucket would make the count disagree with `docker compose ls`.
func Projects(containers []Container) []Project {
	byName := make(map[string]*Project)
	svcSeen := make(map[string]map[string]bool)

	for _, c := range containers {
		if c.Project == "" {
			continue
		}
		p, ok := byName[c.Project]
		if !ok {
			p = &Project{
				Name:        c.Project,
				Age:         c.Age(),
				WorkingDir:  c.Labels[LabelWorkingDir],
				ConfigFiles: splitLabelList(c.Labels[LabelConfigFiles]),
				EnvFiles:    splitLabelList(c.Labels[LabelEnvFiles]),
			}
			byName[c.Project] = p
			svcSeen[c.Project] = make(map[string]bool)
		}
		p.Total++
		if c.Running() {
			p.Running++
		} else {
			p.Stopped++
		}
		if c.Service != "" && !svcSeen[c.Project][c.Service] {
			svcSeen[c.Project][c.Service] = true
			p.Services = append(p.Services, c.Service)
		}
	}

	out := make([]Project, 0, len(byName))
	for _, p := range byName {
		sort.Strings(p.Services)
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Status renders a project's health as "running/total".
func (p Project) Status() string { return fmt.Sprintf("%d/%d", p.Running, p.Total) }

// splitLabelList splits a comma-separated compose label into its paths.
func splitLabelList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ServiceList is the comma-joined service names, for the table column.
func (p Project) ServiceList() string { return strings.Join(p.Services, ",") }
