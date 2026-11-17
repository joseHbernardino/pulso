package logutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTailLines(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		n           int
	}{
		{"one\ntwo\nthree\n", "two\nthree\n", 2},
		{"one\ntwo\nthree", "two\nthree", 2},
		{"one\n", "one\n", 1},
		{"one\n\n", "\n", 1},
		{"", "", 3},
	} {
		if got := TailLines(tc.input, tc.n); got != tc.want {
			t.Errorf("TailLines(%q,%d)=%q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestReadFileBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("1234567890"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFileBounded(path, 4)
	if err != nil || string(data) != "7890" {
		t.Fatalf("tail=%q error=%v", data, err)
	}
}
