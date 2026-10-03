// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

// UsedByParam packs what "used_by" needs: the kind of object, the key that
// identifies it to a container (an image's full ID, a volume's or a
// network's name), and the name to show.
func UsedByParam(kind, key, name string) string { return kind + "\x00" + key + "\x00" + name }
