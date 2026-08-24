package vfcurve

import (
	"os"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestParseStockCurve(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Count != 127 {
		t.Errorf("Count = %d, want 127", c.Count)
	}
	// Full curve starts at 450mV (my earlier Python parser started at 675mV
	// due to a wrong 888-char header assumption - regression test).
	if got := c.Points[0].Voltage; got != 450 {
		t.Errorf("first point voltage = %v mV, want 450", got)
	}
	if len(c.Points) != 127 {
		t.Errorf("len(Points) = %d, want 127", len(c.Points))
	}
	// Stock curve: all offsets zero.
	for i, p := range c.Points {
		if p.Offset != 0 {
			t.Errorf("point %d (%.0fmV): offset = %v, want 0", i, p.Voltage, p.Offset)
		}
	}
	// Spot-check known stock values from the RTX 5090 FE.
	stock900, ok := c.StockAt(900)
	if !ok {
		t.Fatal("StockAt(900): not found")
	}
	if stock900 != 1920 {
		t.Errorf("stock @ 900mV = %v, want 1920", stock900)
	}
	stock675, ok := c.StockAt(675)
	if !ok || stock675 != 180 {
		t.Errorf("stock @ 675mV = %v (ok=%v), want 180", stock675, ok)
	}
	// Monotonic.
	for i := 1; i < len(c.Points); i++ {
		if c.Points[i].Freq < c.Points[i-1].Freq {
			t.Errorf("non-monotonic stock at %vmV", c.Points[i].Voltage)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	orig := loadFixture(t, "profile2_vfcurve.hex")
	c, err := Parse(orig)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := c.Encode()
	if out != orig {
		t.Fatalf("round-trip mismatch:\n got len %d\nwant len %d", len(out), len(orig))
	}
}

func TestParseDetectsModifiedCurve(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile1_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Profile1 is the old 2827MHz @ 900mV attempt: +907 offset in mid-range.
	stock900, ok := c.StockAt(900)
	if !ok {
		t.Fatal("StockAt(900): not found")
	}
	for _, p := range c.Points {
		if p.Voltage == 900 {
			if p.Offset != 907 {
				t.Errorf("offset @ 900mV = %v, want 907", p.Offset)
			}
			if p.Final() != 2827 {
				t.Errorf("final @ 900mV = %v, want 2827 (freq=%v)", p.Final(), stock900)
			}
		}
	}
}

func TestValidateNotch(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile1_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	issues := c.Validate(850, 10, 200)
	if len(issues) == 0 {
		t.Fatal("expected issues in known-broken Profile1, got none")
	}
	kinds := map[string]bool{}
	for _, is := range issues {
		kinds[is.Kind] = true
	}
	// Profile1 has +907 offset starting at 810mV - a notch/boost at low V.
	if !kinds["notch"] {
		t.Errorf("expected notch issue, got %v", kinds)
	}
}

func TestValidateCleanStock(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Stock has big adjacent jumps at the low end (idle steps) but nothing
	// in the operating range. Validate only >= 800mV to keep it meaningful.
	c.Points = c.Points[:0] // placeholder to prove validation shape below
	_ = c
	// Re-parse and check no notches (offsets are all zero).
	c2, _ := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	for _, is := range c2.Validate(800, 10, 1e9) {
		if is.Kind == "notch" {
			t.Errorf("unexpected notch in stock curve: %+v", is)
		}
	}
}

func TestEncodeDeterministic(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Encode() != c.Encode() {
		t.Fatal("Encode not deterministic")
	}
}
