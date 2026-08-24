//go:build windows

package nvapi

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	dll            = syscall.NewLazyDLL("nvapi64.dll")
	procQueryIface *syscall.LazyProc
)

// NVAPI function IDs (reverse-engineered).
const (
	idInitialize            = 0x0150E828
	idUnload                = 0xD22BDD7E
	idEnumPhysicalGPUs      = 0xE5AC921F
	idGetFullName           = 0xCEEE8E9F
	idClkVfPointsGetStatus  = 0x21537AD4
	idClkVfPointsGetInfo    = 0x507B4B59
	idClkVfPointsGetControl = 0x23F1B133
	idClkVfPointsSetControl = 0x0733E009
	idClientVoltRailsStatus = 0x465F9BCF
	idGetErrorMessage       = 0x6C2D048C
	// Legacy/pre-Blackwell (nvapioc method)
	idGetClockBoostMask  = 0x507B4B59 // Same as ClkVfPointsGetInfo
	idGetVFPCurve        = 0x21537AD4 // Same as ClkVfPointsGetStatus
	idGetClockBoostTable = 0x23F1B133 // Same as ClkVfPointsGetControl
	idSetClockBoostTable = 0x0733E009 // Same as ClkVfPointsSetControl
)

// Status codes.
const (
	NVAPI_OK                      = 0
	NVAPI_ERROR                   = -1
	NVAPI_INCOMPATIBLE_STRUCT_VER = -9
)

// Struct sizes from PenguinBurner hidden_nvapi_vf.py (RTX 5090).
const (
	statusSize  = 0x1C28 // 7208 bytes
	controlSize = 0x2420 // 9248 bytes
	pointCount  = 255    // not 128
)

// queryInterface resolves a private NVAPI function pointer by ID.
func queryInterface(id uint32) (uintptr, error) {
	if procQueryIface == nil {
		if err := dll.Load(); err != nil {
			return 0, fmt.Errorf("nvapi: load nvapi64.dll: %w", err)
		}
		procQueryIface = dll.NewProc("nvapi_QueryInterface")
	}
	ptr, _, errno := procQueryIface.Call(uintptr(id))
	if ptr == 0 {
		return 0, fmt.Errorf("nvapi: interface 0x%08x not exposed (errno %v)", id, errno)
	}
	return ptr, nil
}

// call invokes a resolved NVAPI function pointer.
func call(fn uintptr, args ...uintptr) (int32, error) {
	ret, _, _ := syscall.SyscallN(fn, args...)
	return int32(ret), nil
}

// GPU handle.
type GPU uintptr

// Session holds NVAPI state.
type Session struct {
	gpu             GPU
	gpuName         string
	inited          bool
	preferBlackwell bool // if false, use pre-Blackwell ClockBoostTable API
}

// Probe reports available interfaces.
type Probe struct {
	Blackwell    bool
	PreBlackwell bool
}

// ProbeInterfaces checks which VF APIs the driver exposes.
func ProbeInterfaces() (*Probe, error) {
	p := &Probe{}
	if _, err := queryInterface(idClkVfPointsGetStatus); err == nil {
		p.Blackwell = true
	}
	if _, err := queryInterface(0x23F1B133); err == nil {
		p.PreBlackwell = true
	}
	// Try pre-Blackwell SetClockBoostTable if Blackwell fails
	if !p.Blackwell && !p.PreBlackwell {
		return p, fmt.Errorf("nvapi: no VF interface exposed")
	}
	return p, nil
}

// Init initializes NVAPI and returns a session.
func Init() (*Session, error) {
	fn, err := queryInterface(idInitialize)
	if err != nil {
		return nil, err
	}
	ret, _ := call(fn)
	if ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: Initialize failed: %d", ret)
	}

	fn, err = queryInterface(idEnumPhysicalGPUs)
	if err != nil {
		return nil, err
	}
	var gpus [64]uintptr
	var count int32
	ret, _ = call(fn, uintptr(unsafe.Pointer(&gpus[0])), uintptr(unsafe.Pointer(&count)))
	if ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: EnumPhysicalGPUs failed: %d", ret)
	}
	if count == 0 {
		return nil, fmt.Errorf("nvapi: no GPUs found")
	}

	var name string
	fn, err = queryInterface(idGetFullName)
	if err == nil {
		var buf [256]byte
		ret, _ = call(fn, gpus[0], uintptr(unsafe.Pointer(&buf[0])))
		if ret == NVAPI_OK {
			for i, b := range buf {
				if b == 0 {
					name = string(buf[:i])
					break
				}
			}
		}
	}

	return &Session{gpu: GPU(gpus[0]), gpuName: name, inited: true}, nil
}

// InitWithAPI initializes with a specific API preference.
func InitWithAPI(preferBlackwell bool) (*Session, error) {
	sess, err := Init()
	if err != nil {
		return nil, err
	}
	sess.preferBlackwell = preferBlackwell
	return sess, nil
}

// Close unloads NVAPI.
func (s *Session) Close() {
	if s.inited {
		if fn, err := queryInterface(idUnload); err == nil {
			call(fn)
		}
		s.inited = false
	}
}

// GPUName returns the GPU model.
func (s *Session) GPUName() string { return s.gpuName }

// VFPoint is one voltage/frequency point.
type VFPoint struct {
	Index     int
	FreqKHz   uint32
	VoltageUV uint32
	OffsetKHz int32
}

// makeVersion packs version and size.
func makeVersion(ver, size uint32) uint32 {
	return (ver << 16) | (size & 0xFFFF)
}

// ReadCurve reads the current VF curve.
func (s *Session) ReadCurve() ([]VFPoint, error) {
	if !s.inited {
		return nil, fmt.Errorf("nvapi: not initialized")
	}

	fn, err := queryInterface(idClkVfPointsGetStatus)
	if err != nil {
		return nil, err
	}

	// Allocate exact-size buffer
	buf := make([]byte, statusSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, statusSize))
	// Mask: all 1s (0x04-0x13)
	for i := 4; i < 20; i++ {
		buf[i] = 0xFF
	}
	// NumClocks at 0x14
	binary.LittleEndian.PutUint32(buf[0x14:], 15)

	ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0])))
	if ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: ClkVfPointsGetStatus failed: %d", ret)
	}

	// Read offsets
	offsets, _ := s.readOffsets()

	// Parse points at 0x48, stride 28 (from Loong0x00 PoC)
	points := make([]VFPoint, 0, 128)
	for i := 0; i < 128; i++ {
		off := 0x48 + i*28
		if off+8 > len(buf) {
			break
		}
		freq := binary.LittleEndian.Uint32(buf[off:])
		volt := binary.LittleEndian.Uint32(buf[off+4:])
		if freq == 0 && volt == 0 {
			continue
		}
		var offset int32
		if i < len(offsets) {
			offset = offsets[i]
		}
		points = append(points, VFPoint{
			Index:     i,
			FreqKHz:   freq,
			VoltageUV: volt,
			OffsetKHz: offset,
		})
	}
	return points, nil
}

// readOffsets reads current per-point frequency offsets.
func (s *Session) readOffsets() ([]int32, error) {
	fn, err := queryInterface(idClkVfPointsGetControl)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, controlSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, controlSize))
	// Mask at 0x04, 32 bytes (8 x uint32)
	for i := 4; i < 36; i++ {
		buf[i] = 0xFF
	}

	ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0])))
	if ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: ClkVfPointsGetControl failed: %d", ret)
	}

	// Offsets at 0x48, stride 36 (not 72)
	offsets := make([]int32, pointCount)
	for i := 0; i < pointCount; i++ {
		off := 0x48 + i*36
		offsets[i] = int32(binary.LittleEndian.Uint32(buf[off+20:])) // +20 = type(4) + rsvd(16)
	}
	return offsets, nil
}

// SetOffset sets the frequency offset for a single VF point.
// Uses Blackwell ClkVfPointsSetControl if available, falls back to pre-Blackwell ClockBoostTable.
func (s *Session) SetOffset(pointIndex int, offsetKHz int32) error {
	if !s.inited {
		return fmt.Errorf("nvapi: not initialized")
	}

	// Try Blackwell first
	if err := s.setOffsetBlackwell(pointIndex, offsetKHz); err == nil {
		return nil
	}

	// Fall back to pre-Blackwell
	return s.setOffsetLegacy(pointIndex, offsetKHz)
}

// setOffsetBlackwell uses the Blackwell ClkVfPointsSetControl API.
func (s *Session) setOffsetBlackwell(pointIndex int, offsetKHz int32) error {
	if pointIndex < 0 || pointIndex >= pointCount {
		return fmt.Errorf("nvapi: point index %d out of range", pointIndex)
	}

	fn, err := queryInterface(idClkVfPointsSetControl)
	if err != nil {
		return err
	}

	buf := make([]byte, controlSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, controlSize))
	// Single-bit mask: vf_points_mask is NvU32[8] at 0x04
	wordIdx := pointIndex / 32
	bitIdx := pointIndex % 32
	maskOff := 0x04 + wordIdx*4
	binary.LittleEndian.PutUint32(buf[maskOff:], 1<<bitIdx)

	// Offset at 0x48 + pointIndex * 36 + 20 (type + rsvd + prog)
	offPos := 0x48 + pointIndex*36 + 20
	binary.LittleEndian.PutUint32(buf[offPos:], uint32(offsetKHz))

	ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0])))
	if ret != NVAPI_OK {
		return fmt.Errorf("nvapi: ClkVfPointsSetControl failed: %d (%s)", ret, s.errorText(ret))
	}
	return nil
}

// errorText looks up an NVAPI error message.
func (s *Session) errorText(status int32) string {
	fn, err := queryInterface(idGetErrorMessage)
	if err != nil {
		return "unknown"
	}
	var buf [64]byte
	ret, _ := call(fn, uintptr(status), uintptr(unsafe.Pointer(&buf[0])))
	if ret != NVAPI_OK {
		return fmt.Sprintf("error %d", status)
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf[:])
}

// setOffsetLegacy uses the pre-Blackwell ClockBoostTable API (nvapioc method).
// Steps: GetClockBoostMask -> GetVFPCurve -> GetClockBoostTable -> SetClockBoostTable
func (s *Session) setOffsetLegacy(pointIndex int, offsetKHz int32) error {
	// Struct sizes from nvapioc
	const (
		maskSize  = 4 + 32 + 32 + 255*24 // version + mask + unknown + clocks[255]
		tableSize = 4 + 32 + 32 + 255*36 // version + mask + unknown + clocks[255]
	)

	// 1. GetClockBoostMask
	maskFn, err := queryInterface(idGetClockBoostMask)
	if err != nil {
		return fmt.Errorf("nvapi: GetClockBoostMask not available: %w", err)
	}
	maskBuf := make([]byte, maskSize)
	binary.LittleEndian.PutUint32(maskBuf[0:], makeVersion(1, maskSize))
	ret, _ := call(maskFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&maskBuf[0])))
	if ret != NVAPI_OK {
		return fmt.Errorf("nvapi: GetClockBoostMask failed: %d (%s)", ret, s.errorText(ret))
	}

	// 2. GetVFPCurve (to verify point exists)
	curveFn, err := queryInterface(idGetVFPCurve)
	if err != nil {
		return fmt.Errorf("nvapi: GetVFPCurve not available: %w", err)
	}
	curveBuf := make([]byte, statusSize)
	binary.LittleEndian.PutUint32(curveBuf[0:], makeVersion(1, statusSize))
	copy(curveBuf[4:36], maskBuf[4:36]) // copy mask
	ret, _ = call(curveFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&curveBuf[0])))
	if ret != NVAPI_OK {
		return fmt.Errorf("nvapi: GetVFPCurve failed: %d (%s)", ret, s.errorText(ret))
	}

	// 3. GetClockBoostTable
	tableFn, err := queryInterface(idGetClockBoostTable)
	if err != nil {
		return fmt.Errorf("nvapi: GetClockBoostTable not available: %w", err)
	}
	tableBuf := make([]byte, tableSize)
	binary.LittleEndian.PutUint32(tableBuf[0:], makeVersion(1, tableSize))
	copy(tableBuf[4:36], maskBuf[4:36]) // copy mask
	ret, _ = call(tableFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&tableBuf[0])))
	if ret != NVAPI_OK {
		return fmt.Errorf("nvapi: GetClockBoostTable failed: %d (%s)", ret, s.errorText(ret))
	}

	// 4. Modify and SetClockBoostTable
	// Offset at 0x44 + pointIndex * 36 + 20 (clockType u32 + unknown[16])
	offPos := 0x44 + pointIndex*36 + 20
	// Note: nvapioc multiplies by 2 (frequencyDeltaKHz * 2)
	binary.LittleEndian.PutUint32(tableBuf[offPos:], uint32(offsetKHz*2))

	setFn, err := queryInterface(idSetClockBoostTable)
	if err != nil {
		return fmt.Errorf("nvapi: SetClockBoostTable not available: %w", err)
	}
	ret, _ = call(setFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&tableBuf[0])))
	if ret != NVAPI_OK {
		return fmt.Errorf("nvapi: SetClockBoostTable failed: %d (%s)", ret, s.errorText(ret))
	}
	return nil
}

// SetAllOffsets sets offsets for multiple points.
// Continues on individual point errors, reports count of failures.
func (s *Session) SetAllOffsets(offsets map[int]int32) error {
	var errs []error
	for idx, off := range offsets {
		if err := s.SetOffset(idx, off); err != nil {
			errs = append(errs, fmt.Errorf("point %d: %w", idx, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d/%d points failed: %v", len(errs), len(offsets), errs[0])
	}
	return nil
}

// ReadVoltage reads current GPU core voltage in µV.
func (s *Session) ReadVoltage() (uint32, error) {
	fn, err := queryInterface(idClientVoltRailsStatus)
	if err != nil {
		return 0, err
	}

	buf := make([]byte, 0x4C)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, 0x4C))

	ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0])))
	if ret != NVAPI_OK {
		return 0, fmt.Errorf("nvapi: ClientVoltRailsGetStatus failed: %d", ret)
	}

	return binary.LittleEndian.Uint32(buf[0x28:]), nil
}
