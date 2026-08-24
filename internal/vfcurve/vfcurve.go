// Package vfcurve parses and generates MSI Afterburner VFCurve blobs.
//
// Blob format (little-endian):
//   - 12-byte header: magic u32 (0x00020000), point count u32 (typically 127),
//     flags u32
//   - N points x 12 bytes: three float32 (voltage mV, frequency MHz, offset MHz)
//   - Zero-padded tail to 6448 hex chars (3224 bytes)
//
// Key semantics (verified against Unwinder's posts on Guru3D and the
// PenguinBurner/Annihil parsers):
//   - The GPU applies stock_freq(at current temperature) + offset. Only the
//     offset is stable; the base curve shifts with temperature (GPU Boost).
//   - The stored frequency is what Afterburner last saw: base(temp) + offset,
//     i.e. the FINAL curve, not the true vBIOS stock. It is display-only.
package vfcurve

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	// HeaderLen is the fixed 12-byte blob header.
	HeaderLen = 12
	// PointLen is the size of one curve point in bytes (3 x float32).
	PointLen = 12
	// MaxPoints is the point count observed in the wild (0x7F).
	MaxPoints = 127
	// BlobBytes is the total blob size Afterburner writes (3224 bytes).
	BlobBytes = 3224

	headerMagic = 0x00020000
)

// Point is one voltage/frequency/offset triple.
type Point struct {
	Voltage float32 // mV
	Freq    float32 // MHz, display-only (final curve at save time)
	Offset  float32 // MHz, the value actually applied
}

// Final returns Freq + Offset (the displayed frequency).
func (p Point) Final() float32 { return p.Freq + p.Offset }

// Curve is a parsed VFCurve blob.
type Curve struct {
	Count  uint32 // point count from header
	Flags  uint32
	Points []Point // only non-zero points
}

// Parse decodes a hex-encoded VFCurve blob.
func Parse(hexBlob string) (*Curve, error) {
	hexBlob = strings.TrimSpace(hexBlob)
	if len(hexBlob) == 0 {
		return nil, errors.New("vfcurve: empty blob")
	}
	if len(hexBlob)%2 != 0 {
		return nil, fmt.Errorf("vfcurve: odd hex length %d", len(hexBlob))
	}
	raw, err := hex.DecodeString(hexBlob)
	if err != nil {
		return nil, fmt.Errorf("vfcurve: decode hex: %w", err)
	}
	if len(raw) < HeaderLen+PointLen {
		return nil, fmt.Errorf("vfcurve: blob too short: %d bytes", len(raw))
	}

	magic := binary.LittleEndian.Uint32(raw[0:4])
	if magic != headerMagic {
		return nil, fmt.Errorf("vfcurve: bad magic 0x%08x (want 0x%08x)", magic, headerMagic)
	}
	c := &Curve{
		Count: binary.LittleEndian.Uint32(raw[4:8]),
		Flags: binary.LittleEndian.Uint32(raw[8:12]),
	}

	for off := HeaderLen; off+PointLen <= len(raw); off += PointLen {
		p := Point{
			Voltage: math.Float32frombits(binary.LittleEndian.Uint32(raw[off:])),
			Freq:    math.Float32frombits(binary.LittleEndian.Uint32(raw[off+4:])),
			Offset:  math.Float32frombits(binary.LittleEndian.Uint32(raw[off+8:])),
		}
		if p.Voltage == 0 && p.Freq == 0 && p.Offset == 0 {
			break // zero-terminated point list
		}
		c.Points = append(c.Points, p)
	}
	if len(c.Points) == 0 {
		return nil, errors.New("vfcurve: no points decoded")
	}
	return c, nil
}

// Encode renders the curve back to Afterburner's hex form, zero-padded to
// BlobBytes.
func (c *Curve) Encode() string {
	buf := make([]byte, 0, BlobBytes)
	var w [4]byte
	writeU32 := func(v uint32) {
		binary.LittleEndian.PutUint32(w[:], v)
		buf = append(buf, w[:]...)
	}
	writeF32 := func(f float32) { writeU32(math.Float32bits(f)) }

	writeU32(headerMagic)
	writeU32(c.Count)
	writeU32(c.Flags)
	for _, p := range c.Points {
		writeF32(p.Voltage)
		writeF32(p.Freq)
		writeF32(p.Offset)
	}
	for len(buf) < BlobBytes {
		buf = append(buf, 0)
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}

// StockAt returns the stored frequency at the given voltage, interpolating
// between neighbouring points. ok=false if the voltage is outside the curve.
func (c *Curve) StockAt(voltage float32) (float32, bool) {
	pts := c.Points
	if len(pts) == 0 || voltage < pts[0].Voltage || voltage > pts[len(pts)-1].Voltage {
		return 0, false
	}
	for i, p := range pts {
		if p.Voltage == voltage {
			return p.Freq, true
		}
		if p.Voltage > voltage {
			prev := pts[i-1]
			t := (voltage - prev.Voltage) / (p.Voltage - prev.Voltage)
			return prev.Freq + t*(p.Freq-prev.Freq), true
		}
	}
	return 0, false
}

// Issue describes a problem found by Validate.
type Issue struct {
	Voltage float32
	Kind    string
	Detail  string
}

// Validate checks the curve for common footguns:
//   - notch: large non-zero offset at low voltage (idle instability)
//   - discontinuity: adjacent final frequencies jumping more than maxJump MHz
//   - non-monotonic finals
func (c *Curve) Validate(lowVMV, maxOff, maxJump float32) []Issue {
	var issues []Issue
	for i, p := range c.Points {
		if p.Voltage < lowVMV && abs(p.Offset) > maxOff {
			issues = append(issues, Issue{
				Voltage: p.Voltage,
				Kind:    "notch",
				Detail:  fmt.Sprintf("offset %+.0f MHz at low voltage (final %.0f vs base %.0f)", p.Offset, p.Final(), p.Freq),
			})
		}
		if i > 0 {
			prev := c.Points[i-1]
			jump := p.Final() - prev.Final()
			if abs(jump) > maxJump {
				issues = append(issues, Issue{
					Voltage: p.Voltage,
					Kind:    "discontinuity",
					Detail:  fmt.Sprintf("final jumps %+.0f MHz from %.0fmV (%.0f -> %.0f)", jump, prev.Voltage, prev.Final(), p.Final()),
				})
			}
			if p.Final() < prev.Final() && abs(p.Final()-prev.Final()) > 1 {
				issues = append(issues, Issue{
					Voltage: p.Voltage,
					Kind:    "non-monotonic",
					Detail:  fmt.Sprintf("final drops %.0f -> %.0f MHz", prev.Final(), p.Final()),
				})
			}
		}
	}
	return issues
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
