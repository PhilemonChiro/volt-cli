package sdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Source is a finished ESP-IDF build of the reference project.
type Source struct {
	BuildDir  string // <project>/build (holds build.ninja)
	IDF       string // IDF_PATH
	Framework string // Volt framework include dir (not packed: {framework})
	Profile   string // the sdkconfig the reference was built with
}

// Pack layout, relative to the pack root.
const (
	dirIDF    = "idf"    // ESP-IDF headers (+ esp_app_desc.c), mirroring IDF_PATH
	dirConfig = "config" // generated sdkconfig.h & co
	dirLib    = "lib"
	dirLD     = "ld"
	dirBin    = "bin"
	dirObj    = "obj"
)

var headerExt = map[string]bool{".h": true, ".hpp": true, ".hh": true, ".hxx": true, ".inc": true, ".def": true, ".tcc": true, ".ipp": true}

// Harvest writes a pack into out (which must not exist) from a reference
// build, returning its spec.
func Harvest(src Source, out string) (*Spec, error) {
	if _, err := os.Stat(out); err == nil {
		return nil, fmt.Errorf("%s already exists", out)
	}
	h := &harvester{src: src, out: out, files: map[string]string{}}
	h.project = filepath.Dir(src.BuildDir)
	s, err := h.run()
	if err != nil {
		os.RemoveAll(out)
		return nil, err
	}
	return s, nil
}

type harvester struct {
	src     Source
	out     string
	project string            // generated project dir (parent of build dir)
	ar      string            // the build's archiver
	files   map[string]string // pack path -> source path, to catch name clashes
}

type compileEntry struct {
	Directory, Command, File string
}

func (h *harvester) run() (*Spec, error) {
	b := h.src.BuildDir
	var cc []compileEntry
	if err := readJSON(filepath.Join(b, "compile_commands.json"), &cc); err != nil {
		return nil, err
	}
	var mainCmd, descCmd *compileEntry
	for i := range cc {
		switch {
		case cc[i].File == filepath.Join(h.project, "main", "main.c"):
			mainCmd = &cc[i]
		case strings.HasSuffix(cc[i].File, "/esp_app_format/esp_app_desc.c"):
			descCmd = &cc[i]
		}
	}
	if mainCmd == nil || descCmd == nil {
		return nil, errors.New("compile_commands.json has no main/main.c or esp_app_desc.c entry")
	}

	s := &Spec{Format: 1}
	main := splitCommand(mainCmd.Command)
	cc0 := filepath.Base(main[0])
	if !strings.HasSuffix(cc0, "gcc") {
		return nil, fmt.Errorf("unexpected compiler %s", main[0])
	}
	s.ToolPrefix = strings.TrimSuffix(cc0, "gcc")
	h.ar = filepath.Join(filepath.Dir(main[0]), s.ToolPrefix+"ar")

	flags, incs, err := h.compileArgs(main[1:])
	if err != nil {
		return nil, err
	}
	s.CFlags, s.Includes = flags, incs
	for _, f := range flags {
		if v, ok := strings.CutPrefix(f, "-DIDF_VER="); ok {
			s.IDFVersion = strings.Trim(v, `"`)
		}
	}

	// App descriptor: per-project source + its exact flags.
	if err := h.checkMembers(filepath.Join(b, "esp-idf", "esp_app_format", "libesp_app_format.a"), "esp_app_desc.c.obj"); err != nil {
		return nil, err
	}
	dflags, dincs, err := h.compileArgs(splitCommand(descCmd.Command)[1:])
	if err != nil {
		return nil, err
	}
	for i, f := range dflags {
		switch {
		case strings.HasPrefix(f, "-DPROJECT_NAME="):
			dflags[i] = `-DPROJECT_NAME="{name}"`
		case strings.HasPrefix(f, "-DPROJECT_VER="):
			dflags[i] = `-DPROJECT_VER="{version}"`
		}
	}
	for _, inc := range dincs {
		dflags = append(dflags, "-I"+inc)
	}
	s.AppDesc.Args = dflags
	if s.AppDesc.Src, err = h.copyIDF(descCmd.File); err != nil {
		return nil, err
	}

	if err := h.link(s); err != nil {
		return nil, err
	}
	if err := h.imageAndFlash(s); err != nil {
		return nil, err
	}
	if err := h.copy(h.src.Profile, "sdkconfig"); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(h.out, SpecFile), s); err != nil {
		return nil, err
	}
	return s, nil
}

// compileArgs splits a compile command's arguments into flags and include
// dirs, copying the headers of every include dir into the pack. Paths that
// identify the builder (output, dependency files, prefix maps) become
// placeholders or are dropped.
func (h *harvester) compileArgs(args []string) (flags, incs []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "-c" || a == "-MT" || a == "-MF":
			i++
		case a == "-MD":
		case a == "-D" && i+1 < len(args): // "-D NAME=v" (CMake writes some this way)
			i++
			flags = append(flags, "-D"+args[i])
		case strings.HasPrefix(a, "-I") || a == "-isystem":
			dir := strings.TrimPrefix(a, "-I")
			if a == "-isystem" {
				return nil, nil, errors.New("-isystem include dirs are not supported")
			}
			inc, err := h.includeDir(dir)
			if err != nil {
				return nil, nil, err
			}
			incs = append(incs, inc)
		case strings.HasPrefix(a, "-fmacro-prefix-map="):
			from, to, _ := strings.Cut(strings.TrimPrefix(a, "-fmacro-prefix-map="), "=")
			switch filepath.Clean(from) {
			case h.project:
				from = "{project}"
			case filepath.Clean(h.src.IDF):
				from = "{pack}/" + dirIDF
			default:
				return nil, nil, fmt.Errorf("unexpected prefix map %s", a)
			}
			flags = append(flags, "-fmacro-prefix-map="+from+"="+to)
		case strings.HasPrefix(a, "-fdebug-prefix-map="):
			// Reproducible builds hide the builder's paths in the pack's own
			// debug info; for Volt's files they would only rename DWARF paths
			// (never loaded), so they are dropped.
		default:
			if strings.Contains(a, h.project) || strings.Contains(a, h.src.IDF) {
				return nil, nil, fmt.Errorf("unexpected path in compile flag %s", a)
			}
			flags = append(flags, a)
		}
	}
	return flags, incs, nil
}

// includeDir copies an include dir's headers and returns its pack form.
func (h *harvester) includeDir(dir string) (string, error) {
	dir = filepath.Clean(dir)
	var dst string
	switch {
	case dir == filepath.Clean(h.src.Framework):
		return "{framework}", nil
	case within(dir, h.src.IDF):
		rel, _ := filepath.Rel(h.src.IDF, dir)
		dst = filepath.Join(dirIDF, rel)
	case within(dir, filepath.Join(h.src.BuildDir, "config")):
		rel, _ := filepath.Rel(filepath.Join(h.src.BuildDir, "config"), dir)
		dst = filepath.Join(dirConfig, rel)
	default:
		return "", fmt.Errorf("include dir outside ESP-IDF and the build: %s", dir)
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !headerExt[filepath.Ext(p)] {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		return h.copy(p, filepath.Join(dst, rel))
	})
	return "{pack}/" + filepath.ToSlash(dst), err
}

func (h *harvester) copyIDF(file string) (string, error) {
	rel, err := filepath.Rel(h.src.IDF, file)
	if err != nil || !within(file, h.src.IDF) {
		return "", fmt.Errorf("%s is not in ESP-IDF", file)
	}
	dst := filepath.Join(dirIDF, rel)
	return "{pack}/" + filepath.ToSlash(dst), h.copy(file, dst)
}

var (
	elfBuildRe = regexp.MustCompile(`(?m)^build (\S+\.elf): CXX_EXECUTABLE_LINKER__\S+ (\S+)`)
	varRe      = regexp.MustCompile(`(?m)^  (\w+) = (.*)$`)
)

// link harvests ESP-IDF's link line from build.ninja.
func (h *harvester) link(s *Spec) error {
	b := h.src.BuildDir
	nj, err := os.ReadFile(filepath.Join(b, "build.ninja"))
	if err != nil {
		return err
	}
	m := elfBuildRe.FindSubmatchIndex(nj)
	if m == nil {
		return errors.New("build.ninja has no ELF link step")
	}
	elfSrc := string(nj[m[4]:m[5]])
	block, _, _ := strings.Cut(string(nj[m[1]:]), "\n\n")
	vars := map[string]string{}
	for _, v := range varRe.FindAllStringSubmatch(block, -1) {
		vars[v[1]] = v[2]
	}

	s.Link.Flags = splitCommand(vars["FLAGS"])
	for _, f := range splitCommand(vars["LINK_FLAGS"]) {
		if strings.HasPrefix(f, "-Wl,--Map=") {
			f = "-Wl,--Map={map}"
		} else if strings.Contains(f, b) || strings.Contains(f, h.src.IDF) {
			return fmt.Errorf("unexpected path in link flag %s", f)
		}
		s.Link.LDFlags = append(s.Link.LDFlags, f)
	}
	if s.Link.ElfSrc, err = h.copyTo(filepath.Join(b, elfSrc), dirObj); err != nil {
		return err
	}

	// Every -L dir collapses into lib/ (archives) and ld/ (linker scripts);
	// names must stay unique for -l and -T lookups to resolve the same way.
	libDirs := splitCommand(vars["LINK_PATH"])
	for i, l := range libDirs {
		libDirs[i] = strings.TrimPrefix(l, "-L")
		lds, _ := filepath.Glob(filepath.Join(libDirs[i], "*.ld"))
		for _, ld := range lds {
			if _, err := h.copyTo(ld, dirLD); err != nil {
				return err
			}
			if err := h.scrubPaths(filepath.Join(dirLD, filepath.Base(ld))); err != nil {
				return err
			}
		}
	}
	s.Link.LibPath = []string{"-L{pack}/" + dirLib, "-L{pack}/" + dirLD}

	libs := splitCommand(vars["LINK_LIBRARIES"])
	for i := 0; i < len(libs); i++ {
		l := libs[i]
		switch {
		case l == "-u":
			s.Link.Libs = append(s.Link.Libs, l, libs[i+1])
			i++
			continue
		case l == "esp-idf/main/libmain.a":
			l = "{app}"
		case l == "esp-idf/esp_app_format/libesp_app_format.a":
			l = "{app_desc}"
		case strings.HasPrefix(l, "-l"):
			name := "lib" + strings.TrimPrefix(l, "-l") + ".a"
			for _, d := range libDirs {
				if p := filepath.Join(d, name); exists(p) {
					if _, err := h.copyTo(p, dirLib); err != nil {
						return err
					}
					break
				}
			}
		case strings.HasSuffix(l, ".a") && !filepath.IsAbs(l):
			// Built archives keep their build-tree path: names repeat
			// (esp-idf/mbedtls/libmbedtls.a, …/mbedtls/library/libmbedtls.a).
			dst := filepath.Join(dirLib, l)
			if err := h.copy(filepath.Join(b, l), dst); err != nil {
				return err
			}
			l = "{pack}/" + filepath.ToSlash(dst)
		case strings.HasSuffix(l, ".a"):
			if l, err = h.copyTo(l, dirLib); err != nil {
				return err
			}
		case strings.HasPrefix(l, "-"):
		default:
			return fmt.Errorf("unexpected link library %s", l)
		}
		s.Link.Libs = append(s.Link.Libs, l)
	}
	return nil
}

var elf2imageRe = regexp.MustCompile(`--chip (\S+) elf2image (.*?) -o `)

// imageAndFlash records the image arguments and copies bootloader and
// partition table with their flash offsets.
func (h *harvester) imageAndFlash(s *Spec) error {
	b := h.src.BuildDir
	nj, err := os.ReadFile(filepath.Join(b, "build.ninja"))
	if err != nil {
		return err
	}
	m := elf2imageRe.FindSubmatch(nj)
	if m == nil {
		return errors.New("build.ninja has no elf2image step")
	}
	s.Chip = string(m[1])
	s.Elf2Image = append([]string{"--chip", s.Chip, "elf2image"}, splitCommand(string(m[2]))...)

	var fa struct {
		FlashSettings map[string]string `json:"flash_settings"`
		Bootloader    struct{ Offset, File string }
		App           struct{ Offset string }
		PartTable     struct{ Offset, File string } `json:"partition-table"`
	}
	if err := readJSON(filepath.Join(b, "flasher_args.json"), &fa); err != nil {
		return err
	}
	s.FlashSettings = fa.FlashSettings
	boot, err := h.copyTo(filepath.Join(b, fa.Bootloader.File), dirBin)
	if err != nil {
		return err
	}
	pt, err := h.copyTo(filepath.Join(b, fa.PartTable.File), dirBin)
	if err != nil {
		return err
	}
	s.Flash = []FlashPart{
		{Name: "bootloader", Offset: fa.Bootloader.Offset, File: boot},
		{Name: "partition-table", Offset: fa.PartTable.Offset, File: pt},
		{Name: "app", Offset: fa.App.Offset},
	}
	return nil
}

// checkMembers makes sure an archive holds exactly the given objects.
func (h *harvester) checkMembers(archive string, want ...string) error {
	out, err := exec.Command(h.ar, "t", archive).Output()
	if err != nil {
		return fmt.Errorf("listing %s: %w", archive, err)
	}
	got := strings.Fields(string(out))
	if strings.Join(got, " ") != strings.Join(want, " ") {
		return fmt.Errorf("%s holds %v, expected %v", filepath.Base(archive), got, want)
	}
	return nil
}

// copyTo copies file into a flat pack dir, returning "{pack}/dir/name".
// scrubPaths rewrites the builder's build dir and ESP-IDF paths in a copied
// text file (generated linker scripts name their template in a comment) to
// "build/" and "/IDF/", like the reproducible build does in the archives.
func (h *harvester) scrubPaths(rel string) error {
	p := filepath.Join(h.out, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	t := strings.ReplaceAll(string(b), h.src.BuildDir+string(filepath.Separator), "build/")
	t = strings.ReplaceAll(t, h.src.IDF+string(filepath.Separator), "/IDF/")
	if t == string(b) {
		return nil
	}
	return os.WriteFile(p, []byte(t), 0o644)
}

func (h *harvester) copyTo(file, dir string) (string, error) {
	dst := filepath.Join(dir, filepath.Base(file))
	return "{pack}/" + filepath.ToSlash(dst), h.copy(file, dst)
}

// copy copies src to the pack path dst once; a different file under the same
// pack path is an error.
func (h *harvester) copy(src, dst string) error {
	src = filepath.Clean(src)
	if prev, ok := h.files[dst]; ok {
		if prev != src {
			return fmt.Errorf("pack path %s clashes: %s and %s", dst, prev, src)
		}
		return nil
	}
	h.files[dst] = src
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	p := filepath.Join(h.out, dst)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	o, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := io.Copy(o, in); err != nil {
		o.Close()
		return err
	}
	return o.Close()
}

// ProfileHash identifies a pack's configuration: the defines of its
// sdkconfig.h (see ConfigDefines).
func ProfileHash(pack string) (string, error) {
	defs, err := ConfigDefines(filepath.Join(pack, dirConfig, "sdkconfig.h"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.Join(defs, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// ConfigDefines returns an sdkconfig.h's #define lines, sorted, without
// CONFIG_IDF_INIT_VERSION: it records which ESP-IDF first wrote the
// sdkconfig, not a build setting.
func ConfigDefines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var defs []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "#define") && !strings.HasPrefix(l, "#define CONFIG_IDF_INIT_VERSION ") {
			defs = append(defs, l)
		}
	}
	sort.Strings(defs)
	return defs, nil
}

// splitCommand splits a shell command line as CMake/Ninja write them:
// whitespace-separated, with "..." and '...' quoting and \ escapes.
func splitCommand(s string) []string {
	var args []string
	var cur strings.Builder
	in, quote := false, rune(0)
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		switch {
		case quote == 0 && (c == ' ' || c == '\t' || c == '\n'):
			if in {
				args = append(args, cur.String())
				cur.Reset()
				in = false
			}
		case c == '\\' && quote != '\'' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case quote == 0 && (c == '"' || c == '\''):
			quote, in = c, true
		case c == quote:
			quote = 0
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	if in {
		args = append(args, cur.String())
	}
	return args
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// CompilerBin returns the compiler bin dir an ESP-IDF build used.
func CompilerBin(buildDir string) (string, error) {
	var cc []compileEntry
	if err := readJSON(filepath.Join(buildDir, "compile_commands.json"), &cc); err != nil {
		return "", err
	}
	if len(cc) == 0 {
		return "", errors.New("compile_commands.json is empty")
	}
	return filepath.Dir(splitCommand(cc[0].Command)[0]), nil
}
