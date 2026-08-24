// Package profile reads and writes MSI Afterburner profile .cfg files.
//
// A profile cfg is INI-like: [Section] headers with Key=Value lines. The
// GPU-specific file lives at
//
//	C:\Program Files (x86)\MSI Afterburner\Profiles\VEN_10DE&DEV_...cfg
//
// and holds [Startup], [Profile1..5], [Defaults], [PreSuspendedMode]
// sections, each with a VFCurve=<hex> line when a curve is saved.
//
// Afterburner must be closed when writing: it holds the config in memory and
// overwrites the file on exit.
package profile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	sectionRe  = regexp.MustCompile(`(?m)^\[(\w+)\]\s*$`)
	vfcurveKey = "VFCurve="
)

// File is a parsed profile cfg.
type File struct {
	path     string
	sections []section
}

type section struct {
	name  string
	lines []string // raw lines after the [header]
}

// Load reads a profile cfg.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile: read %s: %w", path, err)
	}
	f := &File{path: path}
	var cur *section
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			f.sections = append(f.sections, section{name: m[1]})
			cur = &f.sections[len(f.sections)-1]
			continue
		}
		if cur != nil {
			cur.lines = append(cur.lines, line)
		}
	}
	if len(f.sections) == 0 {
		return nil, fmt.Errorf("profile: no sections in %s", path)
	}
	return f, nil
}

// Sections lists section names in order.
func (f *File) Sections() []string {
	out := make([]string, len(f.sections))
	for i, s := range f.sections {
		out[i] = s.name
	}
	return out
}

// VFCurve returns the hex blob for a section, or "" if unset/empty.
func (f *File) VFCurve(name string) string {
	for _, s := range f.sections {
		if s.name != name {
			continue
		}
		for _, line := range s.lines {
			if strings.HasPrefix(line, vfcurveKey) {
				return strings.TrimPrefix(line, vfcurveKey)
			}
		}
	}
	return ""
}

// Key returns any key value from a section ("" if missing).
func (f *File) Key(section, key string) string {
	for _, s := range f.sections {
		if s.name != section {
			continue
		}
		for _, line := range s.lines {
			if strings.HasPrefix(line, key+"=") {
				return strings.TrimPrefix(line, key+"=")
			}
		}
	}
	return ""
}

// SetVFCurve sets (or replaces) the VFCurve for a section, creating the
// section with Afterburner's default keys if it does not exist.
func (f *File) SetVFCurve(name, hexBlob string) error {
	if hexBlob == "" {
		return errors.New("profile: refusing to set empty VFCurve")
	}
	for i := range f.sections {
		s := &f.sections[i]
		if s.name != name {
			continue
		}
		for j, line := range s.lines {
			if strings.HasPrefix(line, vfcurveKey) {
				s.lines[j] = vfcurveKey + hexBlob
				return nil
			}
		}
		// Section exists but has no VFCurve line: insert after Format/PowerLimit/CoreClkBoost.
		insertAt := 0
		for j, line := range s.lines {
			if strings.HasPrefix(line, "Format=") || strings.HasPrefix(line, "PowerLimit=") || strings.HasPrefix(line, "CoreClkBoost=") {
				insertAt = j + 1
			}
		}
		s.lines = append(s.lines[:insertAt], append([]string{vfcurveKey + hexBlob}, s.lines[insertAt:]...)...)
		return nil
	}
	// Create new section mirroring Afterburner's default profile shape.
	f.sections = append(f.sections, section{
		name: name,
		lines: []string{
			"Format=2",
			"PowerLimit=100",
			"CoreClkBoost=0",
			vfcurveKey + hexBlob,
			"MemClkBoost=0",
			"FanMode=1",
			"FanSpeed=30",
			"FanMode2=",
			"FanSpeed2=",
			"CoreVoltageBoost=0",
		},
	})
	return nil
}

// Save writes the cfg back to its original path. Line endings are preserved
// as LF; Afterburner accepts both.
func (f *File) Save() error {
	var b strings.Builder
	for _, s := range f.sections {
		b.WriteString("[" + s.name + "]\n")
		for _, line := range s.lines {
			b.WriteString(line + "\n")
		}
	}
	if err := os.WriteFile(f.path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("profile: write %s: %w (Afterburner must be closed; file needs admin on Windows)", f.path, err)
	}
	return nil
}
