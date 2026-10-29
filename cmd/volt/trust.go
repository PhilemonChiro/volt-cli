package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PhilemonChiro/volt-cli/internal/build"
	"github.com/PhilemonChiro/volt-cli/internal/libtrust"
	"github.com/PhilemonChiro/volt-cli/internal/sdkfiles"
)

// libraryDirs resolves the device library and the framework beside it.
func libraryDirs(devicesFlag string) (devs, framework string, err error) {
	devs = useDevices(devicesFlag)
	if devs == "" {
		return "", "", errors.New("no device library found: pass --devices or set $VOLT_DEVICES")
	}
	framework = filepath.Join(filepath.Dir(devs), "framework")
	if !isDir(framework) {
		return "", "", fmt.Errorf("no framework next to the device library (%s)", framework)
	}
	return devs, framework, nil
}

// checkApprovals refuses a build whose device libraries or framework differ
// from what the user approved. With approvals never set up (a CLI-only
// install), it warns and lets the build go on.
func checkApprovals(devs, framework string, r *build.Reporter) error {
	dir, err := libtrust.Dir()
	if err != nil {
		return err
	}
	st, pub, ok, err := libtrust.Load(dir)
	if err != nil {
		return fmt.Errorf("Library approvals are damaged (%v). Open Volt IDE → Library to repair them", err)
	}
	if !ok {
		r.Message("warn", "Library approvals are not set up, so device libraries were not checked. Open Volt IDE once to set them up.")
		return nil
	}
	libs, err := libtrust.Libraries(devs, framework)
	if err != nil {
		return err
	}
	var bad []string
	for _, s := range libtrust.Check(st, pub, libs, nil) {
		name := s.Name
		if name == libtrust.Framework {
			name = "the Volt framework"
		}
		switch s.State {
		case "changed":
			bad = append(bad, name+" changed since you approved it")
		case "unapproved":
			bad = append(bad, name+" was never approved")
		case "invalid":
			bad = append(bad, name+"'s approval record is invalid (tampered?)")
		case "unreadable":
			bad = append(bad, name+": "+s.Detail)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	for _, b := range bad {
		r.Message("error", b)
	}
	what := "1 library is"
	if len(bad) > 1 {
		what = fmt.Sprintf("%d libraries are", len(bad))
	}
	return fmt.Errorf("Build stopped: %s not approved. Review it in Volt IDE (AI agents: call library_request_approval)", what)
}

func runTrust(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: volt trust status|init|approve|revoke|digest [--json]")
		return 2
	}
	sub, args := args[0], args[1:]
	fl, devices := newFlags("trust "+sub, "")
	asJSON := fl.Bool("json", false, "print JSON")
	force := fl.Bool("force", false, "init: replace an existing key (drops all approvals)")
	all := fl.Bool("all", false, "approve: every library present")
	source := fl.String("source", "local", "approve: sdk-baseline, local, ai or registry:<keyId>")
	ver := fl.String("version", "", "approve: library version to record")
	pos, ok := parse(fl, args)
	if !ok {
		return 2
	}
	dir, err := libtrust.Dir()
	if err == nil {
		err = trustCmd(sub, dir, pos, *devices, *asJSON, *force, *all, *source, *ver)
	}
	if err != nil {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
		} else {
			stderr.Println("volt trust: " + err.Error())
		}
		return 1
	}
	return 0
}

func trustCmd(sub, dir string, names []string, devicesFlag string, asJSON, force, all bool, source, ver string) error {
	switch sub {
	case "init":
		priv, err := libtrust.Init(dir, force)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(priv) // the IDE seals this in the keychain
		return err
	case "revoke":
		return libtrust.Revoke(dir, names)
	case "digest":
		if len(names) != 1 {
			return errors.New("usage: volt trust digest <dir>")
		}
		h, err := libtrust.Digest(os.DirFS(names[0]), ".")
		if err == nil {
			fmt.Println(h)
		}
		return err
	case "status", "approve":
	default:
		return fmt.Errorf("unknown subcommand %q", sub)
	}

	devs, framework, err := libraryDirs(devicesFlag)
	if err != nil {
		return err
	}
	libs, err := libtrust.Libraries(devs, framework)
	if err != nil {
		return err
	}

	if sub == "approve" {
		if !all {
			want := map[string]bool{}
			for _, n := range names {
				want[n] = true
			}
			var pick []libtrust.Library
			for _, l := range libs {
				if want[l.Name] {
					pick = append(pick, l)
					delete(want, l.Name)
				}
			}
			for n := range want {
				return fmt.Errorf("no library %q", n)
			}
			if len(pick) == 0 {
				return errors.New("name the libraries to approve, or pass --all")
			}
			libs = pick
		}
		priv, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<14))
		if err != nil {
			return err
		}
		return libtrust.Approve(dir, priv, libs, source, ver)
	}

	st, pub, ok, err := libtrust.Load(dir)
	if err != nil {
		return err
	}
	var list []libtrust.Status
	if ok {
		list = libtrust.Check(st, pub, libs, libtrust.BundledDigests(sdkfiles.FS()))
	}
	if asJSON {
		out := map[string]any{"setUp": ok, "devices": devs, "framework": framework, "libraries": list}
		if ok {
			out["key"] = st.Key
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if !ok {
		fmt.Println("Library approvals are not set up (open Volt IDE once).")
		return nil
	}
	fmt.Println("Key", st.Key)
	for _, s := range list {
		extra := s.Source
		if s.Detail != "" {
			extra = s.Detail
		}
		fmt.Printf("  %-12s %-24s %s\n", s.State, s.Name, strings.TrimSpace(extra))
	}
	return nil
}
