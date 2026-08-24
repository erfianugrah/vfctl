//go:build !windows

package nvapi

import "fmt"

// Stub for non-Windows platforms.

type Probe struct {
	Blackwell    bool
	PreBlackwell bool
}

type Session struct{}
type VFPoint struct {
	Index     int
	FreqKHz   uint32
	VoltageUV uint32
	OffsetKHz int32
}

func ProbeInterfaces() (*Probe, error) {
	return nil, fmt.Errorf("nvapi: only available on Windows")
}

func Init() (*Session, error) {
	return nil, fmt.Errorf("nvapi: only available on Windows")
}

func (s *Session) Close()          {}
func (s *Session) GPUName() string { return "" }
func (s *Session) ReadCurve() ([]VFPoint, error) {
	return nil, fmt.Errorf("nvapi: only available on Windows")
}
func (s *Session) SetOffset(pointIndex int, offsetKHz int32) error {
	return fmt.Errorf("nvapi: only available on Windows")
}
func (s *Session) SetAllOffsets(offsets map[int]int32) error {
	return fmt.Errorf("nvapi: only available on Windows")
}
func (s *Session) ReadVoltage() (uint32, error) {
	return 0, fmt.Errorf("nvapi: only available on Windows")
}
