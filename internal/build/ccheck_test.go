package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The C that broke a real build (int(x) pasted into C): the check reports it
// at the Volt line the #line directive names.
func TestSyntaxCheckMapsToVoltLine(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	fw := t.TempDir()
	os.MkdirAll(filepath.Join(fw, "include"), 0o755)
	code := "#include <stdio.h>\nstatic char buf[32];\nvoid f(float lpg) {\n#line 54 \"main.volt\"\n" +
		"    (void)(snprintf(buf, sizeof(buf), \"Gas:%ld\", (long)(int(lpg))), buf);\n}\n"
	errs, ran, err := SyntaxCheck(code, fw, "", "/x/main.volt")
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if len(errs) == 0 || errs[0].Line != 54 {
		t.Fatalf("want an error on line 54, got %+v", errs)
	}
	ok, _, _ := SyntaxCheck("int main(void) { return 0; }\n", fw, "", "main.volt")
	if len(ok) != 0 {
		t.Fatalf("valid C reported: %+v", ok)
	}
}
