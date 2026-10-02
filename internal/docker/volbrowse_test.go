package docker

import (
	"strings"
	"testing"
)

func TestParseVolumeList(t *testing.T) {
	out := []byte("regular file|6|1700000000|-rw-r--r--|./readme.txt\n" +
		"directory|22|1700000000|drwxr-xr-x|./conf\n" +
		"symbolic link|10|1700000000|lrwxrwxrwx|./link\n" +
		"regular empty file|0|1700000000|-rw-r--r--|./we|ird\n" +
		"garbage\n")
	es := parseVolumeList(out)
	got := make([]string, 0, len(es))
	for _, e := range es {
		got = append(got, e.Name+":"+e.Type)
	}
	want := "conf:dir link:link readme.txt:file we|ird:file"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("parsed %s\nwant   %s", s, want)
	}
	if es[2].Size != 6 || es[2].Modified.Unix() != 1700000000 {
		t.Errorf("readme: %+v", es[2])
	}
}

func TestVolumePathStaysInTheVolume(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/": "/", "conf/": "/conf", "../../etc": "/etc", "a/../../b": "/b"} {
		if got := VolumePath(in); got != want {
			t.Errorf("VolumePath(%q) = %q, want %q", in, got, want)
		}
	}
}
