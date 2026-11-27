package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMove(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "a"), filepath.Join(d, "b")
	os.WriteFile(src, []byte("data"), 0o600)
	if err := move(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" {
		t.Fatalf("dst = %q", b)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("src still exists")
	}
	if err := move(filepath.Join(d, "missing"), filepath.Join(d, "c")); err == nil {
		t.Fatal("want error for missing src")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"a:b?.mp4": "a_b_.mp4", "ok_ไทย.mp4": "ok_ไทย.mp4", "x|y*z": "x_y_z"} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
