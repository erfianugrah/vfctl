package profile

import (
	"os"
	"strings"
	"testing"

	"github.com/erfianugrah/vfctl/internal/vfcurve"
)

func loadTestFile(t *testing.T) *File {
	t.Helper()
	f, err := Load("../../testdata/afterburner_profile.cfg")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestLoadSections(t *testing.T) {
	f := loadTestFile(t)
	want := map[string]bool{
		"Startup": true, "Profile1": true, "Profile2": true,
		"Profile3": true, "Profile4": true,
	}
	for _, s := range f.Sections() {
		delete(want, s)
	}
	if len(want) > 0 {
		t.Errorf("missing sections: %v (have %v)", want, f.Sections())
	}
}

func TestVFCurveRoundTrip(t *testing.T) {
	f := loadTestFile(t)
	orig := f.VFCurve("Profile2")
	if orig == "" {
		t.Fatal("Profile2 VFCurve empty")
	}
	if _, err := vfcurve.Parse(orig); err != nil {
		t.Fatalf("Profile2 VFCurve does not parse: %v", err)
	}
}

func TestSetVFCurveExisting(t *testing.T) {
	f := loadTestFile(t)
	orig := f.VFCurve("Profile3")
	if orig == "" {
		t.Skip("Profile3 empty in fixture")
	}
	const marker = "DEADBEEF"
	if err := f.SetVFCurve("Profile3", marker); err != nil {
		t.Fatalf("SetVFCurve: %v", err)
	}
	if got := f.VFCurve("Profile3"); got != marker {
		t.Errorf("got %q, want %q", got, marker)
	}
	// Other profiles untouched.
	if f.VFCurve("Profile2") == "" {
		t.Error("Profile2 lost its curve")
	}
}

func TestSetVFCurveNewSection(t *testing.T) {
	f := loadTestFile(t)
	const marker = "CAFEBABE"
	if err := f.SetVFCurve("Profile5", marker); err != nil {
		t.Fatalf("SetVFCurve: %v", err)
	}
	if got := f.VFCurve("Profile5"); got != marker {
		t.Errorf("got %q, want %q", got, marker)
	}
	// New section has the default keys Afterburner expects.
	if got := f.Key("Profile5", "Format"); got != "2" {
		t.Errorf("Profile5 Format = %q, want 2", got)
	}
}

func TestSavePreservesParseability(t *testing.T) {
	f := loadTestFile(t)
	orig2 := f.VFCurve("Profile2")
	if err := f.SetVFCurve("Profile3", orig2); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir() + "/out.cfg"
	f.path = tmp
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, _ := os.ReadFile(tmp)
	f2, err := Load(tmp)
	if err != nil {
		t.Fatalf("reload: %v\n%s", err, b)
	}
	if f2.VFCurve("Profile3") != orig2 {
		t.Error("Profile3 mismatch after save/reload")
	}
	if !strings.Contains(string(b), "[Profile3]") {
		t.Error("saved file missing [Profile3]")
	}
}
