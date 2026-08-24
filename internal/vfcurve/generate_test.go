package vfcurve

import (
	"fmt"
	"testing"
)

func TestUndervolt(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	uv, err := Undervolt(c, 900, 2797, 850)
	if err != nil {
		t.Fatalf("Undervolt: %v", err)
	}

	// Below ramp: untouched.
	for _, p := range uv.Points {
		if p.Voltage < 850 && p.Offset != 0 {
			t.Errorf("offset at %.0fmV = %v, want 0 (below ramp)", p.Voltage, p.Offset)
		}
	}
	// At target: final == 2797.
	for _, p := range uv.Points {
		if p.Voltage == 900 && p.Final() != 2797 {
			t.Errorf("final at 900mV = %v, want 2797", p.Final())
		}
	}
	// Above target: flat.
	for _, p := range uv.Points {
		if p.Voltage >= 900 && p.Final() != 2797 {
			t.Errorf("final at %.0fmV = %v, want flat 2797", p.Voltage, p.Final())
		}
	}
	// Ramp: slope per mV must not exceed the stock curve's own slope.
	// The stock curve skips voltage points (850->860, 875->885), so a
	// per-point jump check false-positives on those gaps.
	const maxSlopePerMV = 32 // stock curve peaks at ~31 MHz/mV
	for i := 1; i < len(uv.Points); i++ {
		if uv.Points[i].Voltage < 845 || uv.Points[i].Voltage > 910 {
			continue
		}
		dv := uv.Points[i].Voltage - uv.Points[i-1].Voltage
		if dv <= 0 {
			continue
		}
		slope := (uv.Points[i].Final() - uv.Points[i-1].Final()) / dv
		if slope > maxSlopePerMV {
			t.Errorf("ramp slope %.1f MHz/mV at %.0fmV", slope, uv.Points[i].Voltage)
		}
	}
	// Encodes back to a valid blob.
	enc := uv.Encode()
	if len(enc) != 6448 {
		t.Errorf("encoded len = %d, want 6448", len(enc))
	}
	if _, err := Parse(enc); err != nil {
		t.Errorf("re-parse encoded: %v", err)
	}
}

func TestUndervoltRejectsBadArgs(t *testing.T) {
	c, err := Parse(loadFixture(t, "profile2_vfcurve.hex"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Undervolt(c, 900, 1000, 850); err == nil {
		t.Error("expected error when target < stock at ramp start")
	}
	if _, err := Undervolt(c, 2000, 3000, 850); err == nil {
		t.Error("expected error for target voltage outside curve")
	}
}

func TestStepTable(t *testing.T) {
	steps := StepTable(2827, 2)
	if len(steps) != 5 {
		t.Fatalf("len = %d, want 5", len(steps))
	}
	want := []float32{2797, 2812, 2827, 2842, 2857}
	for i, w := range want {
		if steps[i] != w {
			t.Errorf("steps[%d] = %v, want %v", i, steps[i], w)
		}
	}
	_ = fmt.Sprint(steps) // keep fmt import if formatting later
}
