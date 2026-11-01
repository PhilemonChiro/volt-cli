package libtrust

import (
	"os"
	"path/filepath"
	"testing"
)

// Two projects with a library of the same name keep separate approvals.
func TestProjectLibrariesKeyedByProject(t *testing.T) {
	var keys []string
	for _, p := range []string{t.TempDir(), t.TempDir()} {
		dir := filepath.Join(p, "devices", "gate_sms")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "device.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		libs, err := ProjectLibraries(p)
		if err != nil || len(libs) != 1 || libs[0].Name != "gate_sms" {
			t.Fatalf("want gate_sms, got %v %v", libs, err)
		}
		keys = append(keys, libs[0].key())
	}
	if keys[0] == keys[1] || keys[0] == "gate_sms" {
		t.Fatalf("project approvals must be keyed per project: %v", keys)
	}
	if libs, err := ProjectLibraries(t.TempDir()); err != nil || libs != nil {
		t.Fatalf("no devices/ folder is fine: %v %v", libs, err)
	}
}
