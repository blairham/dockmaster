package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestFileChanges(t *testing.T) {
	got := fileChanges([]container.FilesystemChange{
		{Path: "/var/log/app.log", Kind: container.ChangeAdd},
		{Path: "/etc/hosts", Kind: container.ChangeModify},
		{Path: "/tmp/old", Kind: container.ChangeDelete},
	})
	want := []FileChange{
		{Path: "/etc/hosts", Kind: "C"},
		{Path: "/tmp/old", Kind: "D"},
		{Path: "/var/log/app.log", Kind: "A"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v (sorted by path, docker diff letters)", i, got[i], want[i])
		}
	}
}
