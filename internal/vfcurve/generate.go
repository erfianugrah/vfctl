package vfcurve

import "fmt"

// Undervolt builds a capped curve from a stock baseline:
//   - below rampFromV: untouched (offset 0) - idle and light load stay stock
//   - rampFromV..targetV: linear ramp from stock@rampFromV to targetFreq
//   - targetV and above: flattened so final = targetFreq everywhere
//
// IMPORTANT (learned the hard way, see 5090-undervolt-journey.md): the stored
// Freq in a saved profile is the FINAL curve at save time, not the vBIOS base.
// Only generate from a known-stock profile (all offsets zero), or the offsets
// you compute will be relative to someone else's final curve.
//
// Also note the stored frequencies are a snapshot at the save-time
// temperature; GPU Boost shifts the base curve with temperature, so the
// effective frequency under load will differ. This is inherent to the
// interface - Afterburner has the same behaviour.
func Undervolt(stock *Curve, targetV, targetFreq, rampFromV float32) (*Curve, error) {
	stockAtRamp, ok := stock.StockAt(rampFromV)
	if !ok {
		return nil, fmt.Errorf("vfcurve: ramp-from %.0fmV outside curve range", rampFromV)
	}
	stockAtTarget, ok := stock.StockAt(targetV)
	if !ok {
		return nil, fmt.Errorf("vfcurve: target %.0fmV outside curve range", targetV)
	}
	if targetFreq <= stockAtRamp {
		return nil, fmt.Errorf("vfcurve: target %.0fMHz must exceed stock %.0fMHz at ramp start %.0fmV",
			targetFreq, stockAtRamp, rampFromV)
	}

	out := &Curve{Count: stock.Count, Flags: stock.Flags}
	for _, p := range stock.Points {
		np := p
		switch {
		case p.Voltage < rampFromV:
			np.Offset = 0
		case p.Voltage < targetV:
			t := (p.Voltage - rampFromV) / (targetV - rampFromV)
			want := stockAtRamp + t*(targetFreq-stockAtRamp)
			np.Offset = want - p.Freq
		default:
			np.Offset = targetFreq - p.Freq
		}
		out.Points = append(out.Points, np)
	}
	_ = stockAtTarget
	return out, nil
}

// StepTable lists valid NVIDIA boost frequency steps around a target.
// Blackwell uses 15MHz steps (effective steps can be 7.5MHz internally, but
// the curve editor snaps to 15).
func StepTable(target float32, steps int) []float32 {
	out := make([]float32, 0, steps*2+1)
	for i := -steps; i <= steps; i++ {
		out = append(out, target+float32(i)*15)
	}
	return out
}
