package curve

import (
	"fmt"
	"strings"

	"github.com/erfianugrah/vfctl/internal/nvapi"
)

// StockAt returns the live stock frequency (MHz) at a voltage point.
func StockAt(points []nvapi.VFPoint, voltage float64) (float64, bool) {
	for _, p := range points {
		if float64(p.VoltageUV)/1000 == voltage {
			return float64(p.FreqKHz) / 1000, true
		}
	}
	return 0, false
}

// BuildOffsets computes per-point offsets (kHz) for a ramp-then-flatten curve.
func BuildOffsets(points []nvapi.VFPoint, voltage, freq, rampFrom float64) (map[int]int32, error) {
	// Bounds guard: refuse values that would push the card outside any sane
	// operating envelope. This is the catastrophic-typo check (freq 27970 instead
	// of 2797) - it is not a tuning limit.
	const (
		minVolt     = 600.0  // mV
		maxVolt     = 1300.0 // mV
		minFreq     = 300.0  // MHz
		maxFreq     = 4000.0 // MHz, above any 5090 clock
		maxOffsetHz = 1500.0 // MHz, per-point offset ceiling
	)
	if voltage < minVolt || voltage > maxVolt {
		return nil, fmt.Errorf("target voltage %.0f mV outside sane range [%.0f, %.0f]", voltage, minVolt, maxVolt)
	}
	if freq < minFreq || freq > maxFreq {
		return nil, fmt.Errorf("target %.0f MHz outside sane range [%.0f, %.0f]", freq, minFreq, maxFreq)
	}

	stock, ok := StockAt(points, voltage)
	if !ok {
		return nil, fmt.Errorf("no VF point at %.0fmV", voltage)
	}
	offsets := make(map[int]int32)
	for _, p := range points {
		mv := float64(p.VoltageUV) / 1000
		var target float64
		switch {
		case mv < rampFrom:
			continue // below ramp: untouched
		case mv < voltage:
			t := (mv - rampFrom) / (voltage - rampFrom)
			target = float64(p.FreqKHz)/1000 + t*(freq-stock)
		default:
			target = freq
		}
		offset := target - float64(p.FreqKHz)/1000
		if offset > maxOffsetHz || offset < -maxOffsetHz {
			return nil, fmt.Errorf("offset %+.0f MHz at %.0fmV exceeds ceiling ±%.0f MHz (base %.0f MHz)", offset, mv, maxOffsetHz, float64(p.FreqKHz)/1000)
		}
		offsets[p.Index] = int32(offset * 1000)
	}
	return offsets, nil
}

// compareOffsets compares read-back offsets against want within one 15 MHz step (15000 kHz);
// reports "missing after write" and "want X got Y" mismatches.
func compareOffsets(after []nvapi.VFPoint, want map[int]int32) error {
	got := make(map[int]int32, len(after))
	for _, p := range after {
		got[p.Index] = p.OffsetKHz
	}
	const tol = 15000 // one 15 MHz step, in kHz
	var mismatches []string
	for idx, w := range want {
		g, present := got[idx]
		if !present {
			mismatches = append(mismatches, fmt.Sprintf("point %d: missing after write", idx))
			continue
		}
		if abs64(int64(g)-int64(w)) > tol {
			mismatches = append(mismatches, fmt.Sprintf("point %d: want %+d kHz, got %+d kHz", idx, w, g))
		}
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("write verification failed: %d/%d points mismatch:\n  %s",
			len(mismatches), len(want), strings.Join(mismatches, "\n  "))
	}
	return nil
}

// VerifyOffsets re-reads the live curve and checks every written offset landed
// within one 15 MHz step. Returns a formatted error listing mismatches.
func VerifyOffsets(sess *nvapi.Session, want map[int]int32) error {
	after, err := sess.ReadCurve()
	if err != nil {
		return fmt.Errorf("read-back after write failed: %w", err)
	}
	if err := compareOffsets(after, want); err != nil {
		return err
	}
	fmt.Printf("verified: %d points applied and read back correctly\n", len(want))
	return nil
}

// ApplyCurve opens NVAPI, reads the live curve, writes the offsets, then reads
// back and verifies they landed. A write that does not verify is an error - the
// card may be in an unknown state, and the caller must not assume success.
func ApplyCurve(voltage, freq, rampFrom float64) error {
	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()
	points, err := sess.ReadCurve()
	if err != nil {
		return err
	}
	offsets, err := BuildOffsets(points, voltage, freq, rampFrom)
	if err != nil {
		return err
	}
	fmt.Printf("applying %.0f MHz @ %.0f mV (%d points)...\n", freq, voltage, len(offsets))
	if err := sess.SetAllOffsets(offsets); err != nil {
		return err
	}
	return VerifyOffsets(sess, offsets)
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func AbsF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
