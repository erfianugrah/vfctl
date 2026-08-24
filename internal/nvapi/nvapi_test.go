//go:build windows

package nvapi

import (
	"testing"
)

// These tests only run on Windows with an NVIDIA GPU.
// They're skipped on Linux or if NVAPI fails to load.

func TestProbeInterfaces(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardware test in short mode")
	}
	probe, err := ProbeInterfaces()
	if err != nil {
		t.Skipf("NVAPI not available: %v", err)
	}
	t.Logf("Blackwell: %v, PreBlackwell: %v", probe.Blackwell, probe.PreBlackwell)
	if !probe.Blackwell && !probe.PreBlackwell {
		t.Error("no VF interface exposed")
	}
}

func TestSessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardware test in short mode")
	}
	sess, err := Init()
	if err != nil {
		t.Skipf("NVAPI init failed: %v", err)
	}
	defer sess.Close()

	if sess.GPUName() == "" {
		t.Error("GPU name empty")
	}
	t.Logf("GPU: %s", sess.GPUName())
}

func TestReadCurve(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardware test in short mode")
	}
	sess, err := Init()
	if err != nil {
		t.Skipf("NVAPI init failed: %v", err)
	}
	defer sess.Close()

	points, err := sess.ReadCurve()
	if err != nil {
		t.Fatalf("ReadCurve: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("no points returned")
	}
	t.Logf("Read %d VF points", len(points))

	// Check for sane values
	for _, p := range points {
		if p.FreqKHz > 4000000 { // > 4000 MHz
			t.Errorf("point %d: freq %d kHz too high", p.Index, p.FreqKHz)
		}
		if p.VoltageUV > 1500000 { // > 1500 mV
			t.Errorf("point %d: voltage %d uV too high", p.Index, p.VoltageUV)
		}
	}

	// Check monotonicity (freq should generally increase with voltage)
	var prevFreq uint32
	for i, p := range points {
		if i > 0 && p.FreqKHz < prevFreq && p.VoltageUV > 0 {
			t.Logf("note: non-monotonic at %d: %d kHz -> %d kHz @ %d uV",
				p.Index, prevFreq, p.FreqKHz, p.VoltageUV)
		}
		prevFreq = p.FreqKHz
	}
}

func TestReadVoltage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardware test in short mode")
	}
	sess, err := Init()
	if err != nil {
		t.Skipf("NVAPI init failed: %v", err)
	}
	defer sess.Close()

	volt, err := sess.ReadVoltage()
	if err != nil {
		t.Fatalf("ReadVoltage: %v", err)
	}
	t.Logf("Current voltage: %d uV (%.0f mV)", volt, float64(volt)/1000)
	if volt < 400000 || volt > 1200000 {
		t.Errorf("voltage %d uV outside expected range", volt)
	}
}

// TestSetOffsetRoundTrip writes then resets an offset.
// Only run with -run TestSetOffsetRoundTrip explicitly.
func TestSetOffsetRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardware test in short mode")
	}
	sess, err := Init()
	if err != nil {
		t.Skipf("NVAPI init failed: %v", err)
	}
	defer sess.Close()

	points, err := sess.ReadCurve()
	if err != nil {
		t.Fatalf("ReadCurve: %v", err)
	}

	// Find a mid-range point (around 900mV)
	var testIdx int
	for _, p := range points {
		mv := float64(p.VoltageUV) / 1000
		if mv >= 890 && mv <= 910 {
			testIdx = p.Index
			break
		}
	}
	if testIdx == 0 {
		t.Skip("no suitable test point found")
	}

	// Read current offset
	origOffsets, err := sess.readOffsets()
	if err != nil {
		t.Fatalf("readOffsets: %v", err)
	}
	orig := origOffsets[testIdx]

	// Set +15 MHz
	testOffset := int32(15000)
	if err := sess.SetOffset(testIdx, testOffset); err != nil {
		t.Fatalf("SetOffset: %v", err)
	}

	// Verify
	newOffsets, err := sess.readOffsets()
	if err != nil {
		t.Fatalf("readOffsets after set: %v", err)
	}
	if newOffsets[testIdx] != testOffset {
		t.Errorf("offset not applied: got %d, want %d", newOffsets[testIdx], testOffset)
	}

	// Reset
	if err := sess.SetOffset(testIdx, orig); err != nil {
		t.Fatalf("failed to reset offset: %v", err)
	}
	t.Logf("round trip OK on point %d", testIdx)
}
