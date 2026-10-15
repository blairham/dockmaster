// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// failingReader yields some bytes, then fails, as a dropped export stream.
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "half a tar"), nil
	}
	return 0, errors.New("connection reset")
}

// TestWriteTarIsAllOrNothing: a failed write leaves an existing file as it
// was and no temp file behind; a good one replaces it, 0600, in a
// directory it makes.
func TestWriteTarIsAllOrNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exports")
	dst := filepath.Join(dir, "web.tar")

	n, err := WriteTar(dst, strings.NewReader("first"))
	if err != nil || n != 5 {
		t.Fatalf("WriteTar = %d, %v", n, err)
	}
	if st, err := os.Stat(dst); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the tar: %v, %v", st, err)
	}

	if _, err := WriteTar(dst, &failingReader{}); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("a failed stream returned %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "first" {
		t.Errorf("a failed write left %q in place of the old file", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("left behind: %v", names)
	}
}

func TestReadLoadStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		err  string
		want []string
	}{
		{
			name: "tags and an untagged ID",
			in:   `{"stream":"Loaded image: nginx:1.27\n"}{"stream":"Loaded image ID: sha256:feed\n"}`,
			want: []string{"nginx:1.27", "sha256:feed"},
		},
		{name: "empty", in: "", want: nil},
		{
			name: "an error after a tag",
			in:   `{"stream":"Loaded image: a:1\n"}{"error":"open /var/lib/x: no space left on device"}`,
			want: []string{"a:1"},
			err:  "no space left",
		},
	} {
		got, err := readLoadStream(io.NopCloser(strings.NewReader(tc.in)))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: loaded %q, want %q", tc.name, got, tc.want)
		}
		if (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
		}
	}
}
