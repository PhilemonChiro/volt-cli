// Package history keeps a private, automatic git history of a Volt project:
// every save, AI edit and outside change becomes a commit whose author says
// who made it (you, Volt Assistant, an AI app, or a change outside the IDE).
//
// The repository lives in <project>/.volt/history with the project folder as
// its work tree, so it never touches a git repository the user keeps
// themselves. It is ordinary git (go-git, no git binary needed): log, diff and
// blame work, and it can be exported later.
package history

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

const email = "history@volt.local"

// Tracked says whether a project file belongs in the history: only the
// project's own files — its .volt program files and volt.toml. Libraries,
// SDK copies, build output, editor/AI settings and every hidden file are the
// platform's to manage, so they are never recorded.
func Tracked(rel string) bool {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if !dirTracked(p) {
			return false
		}
	}
	name := parts[len(parts)-1]
	if strings.HasPrefix(name, ".") {
		return false
	}
	return name == "volt.toml" || strings.HasSuffix(name, ".volt")
}

func dirTracked(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	switch name {
	case "lib", "libs", "sdk", "devices", "framework", "build", "node_modules", "vendor":
		return false
	}
	return true
}

// Repo is one project's history.
type Repo struct {
	dir  string
	repo *git.Repository
}

// Open opens (creating on first use) the history of the project in dir.
func Open(dir string) (*Repo, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a project folder", dir)
	}
	store := filesystem.NewStorage(osfs.New(filepath.Join(dir, ".volt", "history")), cache.NewObjectLRUDefault())
	wt := osfs.New(dir)
	r, err := git.Open(store, wt)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		r, err = git.Init(store, wt)
	}
	if err != nil {
		return nil, err
	}
	return &Repo{dir: dir, repo: r}, nil
}

func (h *Repo) worktree() (*git.Worktree, error) { return h.repo.Worktree() }

// stage makes the index hold exactly the project's tracked files as they are
// on disk (dropping anything recorded earlier that isn't tracked) and returns
// the names that differ from the last recorded change.
func (h *Repo) stage(wt *git.Worktree) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(h.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != h.dir && !dirTracked(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(h.dir, p)
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() || !Tracked(rel) {
			return nil
		}
		if _, err := wt.Add(rel); err != nil {
			return err
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	idx, err := h.repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	keep := idx.Entries[:0]
	for _, e := range idx.Entries {
		if seen[e.Name] {
			keep = append(keep, e)
		}
	}
	idx.Entries = keep
	if err := h.repo.Storer.SetIndex(idx); err != nil {
		return nil, err
	}
	now := map[string]plumbing.Hash{}
	for _, e := range idx.Entries {
		now[e.Name] = e.Hash
	}
	before := map[string]plumbing.Hash{}
	if head, err := h.repo.Head(); err == nil {
		if hc, err := h.repo.CommitObject(head.Hash()); err == nil {
			if t, err := hc.Tree(); err == nil {
				t.Files().ForEach(func(f *object.File) error { before[f.Name] = f.Hash; return nil })
			}
		}
	}
	var changed []string
	for n, hsh := range now {
		if before[n] != hsh {
			changed = append(changed, n)
		}
	}
	for n := range before {
		if _, ok := now[n]; !ok {
			changed = append(changed, n)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// coalesceWithin merges a save into the previous change when the same author
// saved again this soon (saves before every build would flood the log).
const coalesceWithin = 3 * time.Minute

// Record commits the project's current files if anything changed. It returns
// the new commit, or "" when there was nothing to record. With coalesce, a
// change by the same author as the last one, within a few minutes, is folded
// into it.
func (h *Repo) Record(author, message string, coalesce ...bool) (string, error) {
	wt, err := h.worktree()
	if err != nil {
		return "", err
	}
	changed, err := h.stage(wt)
	if err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return "", nil
	}
	if author == "" {
		author = "Unknown"
	}
	if message == "" {
		message = "Changed " + strings.Join(changedNames(changed), ", ")
	}
	amend := false
	if len(coalesce) > 0 && coalesce[0] {
		if head, err := h.repo.Head(); err == nil {
			if hc, err := h.repo.CommitObject(head.Hash()); err == nil && hc.Author.Name == author &&
				time.Since(hc.Author.When) < coalesceWithin && hc.NumParents() > 0 {
				amend = true
				if message == "" || strings.HasPrefix(message, "Changed ") {
					message = strings.TrimSpace(hc.Message)
				}
			}
		}
	}
	sig := &object.Signature{Name: author, Email: email, When: time.Now()}
	hash, err := wt.Commit(message, &git.CommitOptions{Author: sig, Committer: sig, Amend: amend})
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func changedNames(paths []string) []string {
	var names []string
	for _, p := range paths {
		names = append(names, filepath.Base(p))
	}
	if len(names) > 3 {
		names = append(names[:3], fmt.Sprintf("+%d more", len(names)-3))
	}
	return names
}

// Entry is one recorded change.
type Entry struct {
	Hash    string   `json:"hash"`
	Author  string   `json:"author"`
	When    string   `json:"when"`
	Message string   `json:"message"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Files   []string `json:"files"`
}

func entry(c *object.Commit) Entry {
	e := Entry{Hash: c.Hash.String(), Author: c.Author.Name, When: c.Author.When.UTC().Format(time.RFC3339), Message: strings.TrimSpace(c.Message), Files: []string{}}
	if stats, err := c.Stats(); err == nil {
		for _, s := range stats {
			if !Tracked(s.Name) {
				continue // recorded before history was limited to project files
			}
			e.Added += s.Addition
			e.Removed += s.Deletion
			e.Files = append(e.Files, s.Name)
		}
	}
	return e
}

// Log lists recorded changes, newest first, optionally only those touching
// file (a path relative to the project).
func (h *Repo) Log(file string, limit int) ([]Entry, error) {
	head, err := h.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	opts := &git.LogOptions{From: head.Hash()}
	if file != "" {
		f := filepath.ToSlash(file)
		opts.FileName = &f
	}
	it, err := h.repo.Log(opts)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	err = it.ForEach(func(c *object.Commit) error {
		if limit > 0 && len(out) >= limit {
			return io.EOF
		}
		e := entry(c)
		if len(e.Files) == 0 && c.NumParents() > 0 {
			return nil // only touched files the history no longer covers
		}
		out = append(out, e)
		return nil
	})
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return out, err
}

// FileChange is one file's content before and after a change ("" = absent).
type FileChange struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
	Status string `json:"status"` // added | removed | modified
}

// Change is a recorded change with its file contents.
type Change struct {
	Entry
	Changes []FileChange `json:"changes"`
}

func (h *Repo) commit(hash string) (*object.Commit, error) {
	c, err := h.repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return nil, fmt.Errorf("no recorded change %s", hash)
	}
	return c, nil
}

func parentTree(c *object.Commit) (*object.Tree, error) {
	if c.NumParents() == 0 {
		return &object.Tree{}, nil
	}
	p, err := c.Parent(0)
	if err != nil {
		return nil, err
	}
	return p.Tree()
}

// Show returns a change and every file it touched, before and after.
func (h *Repo) Show(hash string) (*Change, error) {
	c, err := h.commit(hash)
	if err != nil {
		return nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	ptree, err := parentTree(c)
	if err != nil {
		return nil, err
	}
	diffs, err := object.DiffTree(ptree, tree)
	if err != nil {
		return nil, err
	}
	out := &Change{Entry: entry(c), Changes: []FileChange{}}
	for _, d := range diffs {
		from, to, err := d.Files()
		if err != nil {
			return nil, err
		}
		fc := FileChange{Status: "modified"}
		if from != nil {
			fc.Path = d.From.Name
			fc.Before, _ = from.Contents()
		} else {
			fc.Status = "added"
		}
		if to != nil {
			fc.Path = d.To.Name
			fc.After, _ = to.Contents()
		} else {
			fc.Status = "removed"
		}
		if Tracked(fc.Path) {
			out.Changes = append(out.Changes, fc)
		}
	}
	return out, nil
}

// BlameLine says which change last touched a line.
type BlameLine struct {
	Line    int    `json:"line"`
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	When    string `json:"when"`
	Message string `json:"message"`
}

// Blame traces every line of file (as last recorded) to its change.
func (h *Repo) Blame(file string) ([]BlameLine, error) {
	head, err := h.repo.Head()
	if err != nil {
		return nil, errors.New("nothing recorded yet")
	}
	c, err := h.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	res, err := git.Blame(c, filepath.ToSlash(file))
	if err != nil {
		return nil, err
	}
	msgs := map[plumbing.Hash]string{}
	out := make([]BlameLine, 0, len(res.Lines))
	for i, l := range res.Lines {
		m, ok := msgs[l.Hash]
		if !ok {
			if lc, err := h.repo.CommitObject(l.Hash); err == nil {
				m = strings.TrimSpace(lc.Message)
			}
			msgs[l.Hash] = m
		}
		out = append(out, BlameLine{Line: i + 1, Hash: l.Hash.String(), Author: l.AuthorName, When: l.Date.UTC().Format(time.RFC3339), Message: m})
	}
	return out, nil
}

func (h *Repo) write(path, content string) error {
	full := filepath.Join(h.dir, filepath.FromSlash(path))
	if !strings.HasPrefix(full, h.dir+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write outside the project: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func (h *Repo) remove(path string) error {
	full := filepath.Join(h.dir, filepath.FromSlash(path))
	if !strings.HasPrefix(full, h.dir+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove outside the project: %s", path)
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Restore puts the project's files (or just file) back as they were right
// after change hash, and records that as a new change by author.
func (h *Repo) Restore(hash, file, author string) (string, error) {
	if _, err := h.Record("Unrecorded changes", ""); err != nil { // keep what's there now
		return "", err
	}
	c, err := h.commit(hash)
	if err != nil {
		return "", err
	}
	tree, err := c.Tree()
	if err != nil {
		return "", err
	}
	want := map[string]string{}
	err = tree.Files().ForEach(func(f *object.File) error {
		if !Tracked(f.Name) || (file != "" && f.Name != filepath.ToSlash(file)) {
			return nil
		}
		s, err := f.Contents()
		want[f.Name] = s
		return err
	})
	if err != nil {
		return "", err
	}
	if file != "" && len(want) == 0 {
		return "", fmt.Errorf("%s didn't exist at that point", file)
	}
	if head, err := h.repo.Head(); err == nil && file == "" {
		if hc, err := h.repo.CommitObject(head.Hash()); err == nil {
			if ht, err := hc.Tree(); err == nil {
				ht.Files().ForEach(func(f *object.File) error {
					if _, keep := want[f.Name]; !keep && Tracked(f.Name) {
						return h.remove(f.Name) // added after that point
					}
					return nil
				})
			}
		}
	}
	for p, s := range want {
		if err := h.write(p, s); err != nil {
			return "", err
		}
	}
	what := "the project"
	if file != "" {
		what = file
	}
	return h.Record(author, fmt.Sprintf("Restored %s to %s (%s)", what, c.Author.When.Local().Format("Jan 2 15:04"), firstLine(c.Message)))
}

// Undo reverses one change, file by file, as long as those files haven't
// changed since; it records the reversal as a new change by author.
func (h *Repo) Undo(hash, author string) (string, error) {
	if _, err := h.Record("Unrecorded changes", ""); err != nil {
		return "", err
	}
	ch, err := h.Show(hash)
	if err != nil {
		return "", err
	}
	for _, fc := range ch.Changes {
		cur, err := os.ReadFile(filepath.Join(h.dir, filepath.FromSlash(fc.Path)))
		exists := err == nil
		if (fc.Status == "removed" && exists) || (fc.Status != "removed" && (!exists || string(cur) != fc.After)) {
			return "", fmt.Errorf("%s has changed since then, so this change can't be undone on its own — restore an earlier version instead", fc.Path)
		}
	}
	for _, fc := range ch.Changes {
		if fc.Status == "added" {
			err = h.remove(fc.Path)
		} else {
			err = h.write(fc.Path, fc.Before)
		}
		if err != nil {
			return "", err
		}
	}
	return h.Record(author, "Undid: "+firstLine(ch.Message))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
