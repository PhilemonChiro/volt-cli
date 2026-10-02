// Package toolchain provisions the build tools each chip needs: manifest
// assets (SHA-256 pinned, from a signed or built-in manifest), pinned SDK
// clones and vendor installers. It is chip-agnostic — each chip family
// registers a factory describing its Components; the drivers here check,
// install and repair any of them the same way.
package toolchain

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Status is a component's current state.
type Status struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"` // version when OK, else what is wrong
	Path   string `json:"path,omitempty"`
}

// Component is one installable piece of a toolchain. Install is nil when
// Volt cannot provide it; Check's Detail then says what the user must do.
type Component struct {
	ID      string
	Name    string
	Check   func() Status
	Install func(e *Emitter) error
}

// Toolchain is everything needed to build for one chip on this host.
type Toolchain struct {
	Chip       string
	Name       string // e.g. "ESP32"
	Components []Component
	// Managed lists what repair deletes. Every entry is under Root(); user
	// provided installs are never listed.
	Managed []string
	// Notes are shown by status and repair (e.g. "using your ESP-IDF at …").
	Notes []string
	// BuildEnv returns the environment overrides the chip's build tools run
	// with. Call it only once Status reports the toolchain ready.
	BuildEnv func() ([]string, error)
}

// Host is the context a factory builds a Toolchain in.
type Host struct {
	Platform       string
	Manifest       *Manifest
	ManifestSource string
}

type factory func(chip string, h *Host) (*Toolchain, error)

var registry = map[string]factory{}

// register adds a chip family. Each chip name is the one volt.toml uses.
func register(f factory, chips ...string) {
	for _, c := range chips {
		registry[c] = f
	}
}

// Chips lists the supported chips.
func Chips() []string {
	var cs []string
	for c := range registry {
		cs = append(cs, c)
	}
	slices.Sort(cs)
	return cs
}

// For returns chip's toolchain. online allows fetching a newer manifest
// (install does; status stays offline and fast).
func For(chip string, online bool) (*Toolchain, error) {
	chip = strings.ToLower(chip)
	f, ok := registry[chip]
	if !ok {
		return nil, fmt.Errorf("no toolchain for chip %q yet (supported: %s)", chip, strings.Join(Chips(), ", "))
	}
	h := &Host{Platform: Platform()}
	if h.Platform == "" {
		return nil, fmt.Errorf("toolchains are not provided for this OS/architecture yet")
	}
	h.Manifest, h.ManifestSource = LoadManifest(online)
	return f(chip, h)
}

// ComponentStatus pairs a component with its state, for reporting.
type ComponentStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Status
}

// Status checks every component.
func (t *Toolchain) Status() (all []ComponentStatus, ready bool) {
	ready = true
	for _, c := range t.Components {
		st := c.Check()
		all = append(all, ComponentStatus{c.ID, c.Name, st})
		ready = ready && st.OK
	}
	return all, ready
}

// Install brings every component up to date, in order, stopping at the first
// failure. Each component is a stage in the event stream.
func (t *Toolchain) Install(e *Emitter) bool {
	for _, c := range t.Components {
		e.StageStart(c.ID, c.Name)
		st := c.Check()
		if st.OK {
			e.Log("Already installed: " + st.Detail)
			e.StageDone(c.ID)
			continue
		}
		if c.Install == nil {
			e.StageError(c.ID, st.Detail)
			e.Complete(false, t.Name+" toolchain is not ready")
			return false
		}
		if err := c.Install(e); err != nil {
			e.StageError(c.ID, err.Error())
			e.Complete(false, t.Name+" toolchain install failed")
			return false
		}
		// Trust the result only if the check now passes.
		if st := c.Check(); !st.OK {
			e.StageError(c.ID, "still not ready after install: "+st.Detail)
			e.Complete(false, t.Name+" toolchain install failed")
			return false
		}
		e.StageDone(c.ID)
	}
	e.Complete(true, t.Name+" toolchain ready")
	return true
}

// Repair deletes the managed state so the next install starts clean. It
// returns what it removed.
func (t *Toolchain) Repair() (removed []string, err error) {
	removed = []string{}
	for _, p := range t.Managed {
		if !within(Root(), p) || p == Root() {
			return removed, fmt.Errorf("refusing to delete %s: not under %s", p, Root())
		}
		if !exists(p) {
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}
