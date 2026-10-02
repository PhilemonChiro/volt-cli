package toolchain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pinnedRepo is an upstream SDK pinned to an exact commit. Cloning a branch
// or a re-pointable tag would let a force-pushed upstream slip unexpected code
// into firmware builds.
type pinnedRepo struct{ url, sha, ref string }

// cloneAtSHA shallow-fetches r at its pinned commit into dest, optionally
// with submodules (pinned transitively by the superproject), then verifies
// HEAD. On any failure dest is removed.
func cloneAtSHA(e *Emitter, r pinnedRepo, dest string, submodules bool) (err error) {
	if !hasCommand("git") {
		return errors.New("git is not installed")
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dest)
		}
	}()
	steps := [][]string{
		{"init", "-q", dest},
		{"-C", dest, "remote", "add", "origin", r.url},
		{"-C", dest, "fetch", "--progress", "--depth", "1", "origin", r.sha},
		{"-C", dest, "-c", "advice.detachedHead=false", "checkout", "-q", r.sha},
	}
	if submodules {
		steps = append(steps, []string{"-C", dest, "submodule", "update", "--init", "--recursive", "--depth", "1", "--progress"})
	}
	for _, args := range steps {
		if err := run(e, "", nil, "git", args...); err != nil {
			return err
		}
	}
	if head := gitHead(dest); head != r.sha {
		return fmt.Errorf("SECURITY: %s HEAD %q != pinned %s; refusing to use it", filepath.Base(dest), head, r.sha)
	}
	e.Log(fmt.Sprintf("Pinned %s @ %s (%.12s)", filepath.Base(dest), r.ref, r.sha))
	return nil
}

// gitHead returns the commit checked out in repo, read from .git/HEAD
// (a pinned clone is always detached, so HEAD holds the SHA).
func gitHead(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		b, _ = os.ReadFile(filepath.Join(repo, ".git", ref))
		head = strings.TrimSpace(string(b))
	}
	return head
}
