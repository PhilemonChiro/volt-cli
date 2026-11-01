package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An official library (one this volt ships) can't be changed by library_write;
// a library made on this computer can.
func TestLockedLibrary(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"bme280", "my_part"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if why := lockedLibrary(root, "bme280"); why == "" {
		t.Fatal("bme280 ships with volt: it must be locked")
	}
	if why := lockedLibrary(root, "my_part"); why != "" {
		t.Fatalf("a local library must stay writable, got %q", why)
	}
	if why := lockedLibrary(root, "not_there"); why != "" {
		t.Fatalf("a new library is writable, got %q", why)
	}
}
