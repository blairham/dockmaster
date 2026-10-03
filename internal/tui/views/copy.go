// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

// Copier is a table view whose selected row has a name and an ID worth
// putting on the clipboard: c copies the name, as k9s's c does, and i the
// full ID (#5). id is "" where the row has no ID of its own.
type Copier interface {
	CopyFields() (name, id string, ok bool)
}

// CopyFields is the selected container's name and full ID.
func (v *ContainersView) CopyFields() (string, string, bool) {
	c, ok := v.Selected()
	return c.Name, c.ID, ok
}

// CopyFields is the selected image's reference and full ID; an untagged
// image's name is its ID.
func (v *ImagesView) CopyFields() (string, string, bool) {
	im, ok := v.Selected()
	name := im.Ref()
	if im.Repo == "" {
		name = im.ID
	}
	return name, im.ID, ok
}

// CopyFields is the selected volume's name, which is also its ID.
func (v *VolumesView) CopyFields() (string, string, bool) {
	vol, ok := v.Selected()
	return vol.Name, vol.Name, ok
}

// CopyFields is the selected network's name and full ID.
func (v *NetworksView) CopyFields() (string, string, bool) {
	n, ok := v.Selected()
	return n.Name, n.ID, ok
}

// CopyFields is the selected compose project's name.
func (v *ProjectsView) CopyFields() (string, string, bool) {
	p, ok := v.Selected()
	return p.Name, "", ok
}

// CopyFields is the selected machine's name.
func (v *RuntimesView) CopyFields() (string, string, bool) {
	m, ok := v.Selected()
	return m.Name, "", ok
}

// CopyFields is the selected Podman pod's name and ID.
func (v *PodsView) CopyFields() (string, string, bool) {
	p, ok := v.Selected()
	return p.Name, p.ID, ok
}

// CopyFields is the selected docker context's name, and its endpoint as the
// thing that identifies it to a client.
func (v *ContextsView) CopyFields() (string, string, bool) {
	c, ok := v.Selected()
	return c.Name, c.Host, ok
}

// CopyFields is the selected pod container's name and containerd ID.
func (v *NodeView) CopyFields() (string, string, bool) {
	c, ok := v.Selected()
	return c.Name, c.ID, ok
}

// CopyFields is the forwarded container's name and the helper container's
// ID.
func (v *PortForwardsView) CopyFields() (string, string, bool) {
	f, ok := v.Selected()
	return f.TargetName, f.ID, ok
}

// CopyFields is the selected save's file name, and its full path.
func (v *DumpsView) CopyFields() (string, string, bool) {
	d, ok := v.Selected()
	return d.Name, d.Path, ok
}
