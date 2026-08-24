//go:build windows

// Package nvapi provides direct GPU VF curve control via undocumented NVAPI.
//
// Struct layouts and function IDs are mirrored from PenguinBurner's
// hidden_nvapi_vf.py (jpietek/PenguinBurner), the write-capable reference
// confirmed working on RTX 5090 (Blackwell). Read paths cross-checked against
// Loong0x00's read-only PoC from LACT issue #936.
//
// Function IDs (nvapi_QueryInterface):
//
//	0x0150E828 - NvAPI_Initialize
//	0xE5AC921F - NvAPI_EnumPhysicalGPUs
//	0xCEEE8E9F - NvAPI_GPU_GetFullName
//	0x507B4B59 - ClockClientClkVfPointsGetInfo   (active mask + point types)
//	0x21537AD4 - ClockClientClkVfPointsGetStatus (freq/voltage per point, v3)
//	0x23F1B133 - ClockClientClkVfPointsGetControl (current offsets, v1)
//	0x0733E009 - ClockClientClkVfPointsSetControl (write offsets, v1)
//	0x465F9BCF - ClientVoltRailsGetStatus         (current voltage)
//	0x6C2D048C - NvAPI_GetErrorMessage
//
// Version field = (version << 16) | (struct_size & 0xFFFF).
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

// NVAPI function IDs.
const (
	idInitialize            = 0x0150E828
	idUnload                = 0xD22BDD7E
	idEnumPhysicalGPUs      = 0xE5AC921F
	idGetFullName           = 0xCEEE8E9F
	idClkVfPointsGetInfo    = 0x507B4B59
	idClkVfPointsGetStatus  = 0x21537AD4
	idClkVfPointsGetControl = 0x23F1B133
	idClkVfPointsSetControl = 0x0733E009
	idClientVoltRailsStatus = 0x465F9BCF
	idGetErrorMessage       = 0x6C2D048C
)

// Status codes.
const (
	NVAPI_OK                      = 0
	NVAPI_ERROR                   = -1
	NVAPI_INCOMPATIBLE_STRUCT_VER = -9
)

// Struct sizes and strides (verified via ctypes, see AGENTS.md).
const (
	infoSize     = 0x182C  // 6188  - ClockClientClkVfPointsInfoV1
	statusSize   = 0x15B0C // 88844 - ClockClientClkVfPointsStatusV3
	controlSize  = 0x2420  // 9248  - ClockClientClkVfPointsControlV1
	pointCount   = 255     // VF_POINTS_CAPACITY
	infoStride   = 24      // ClockClientClkVfPointInfoV1
	statusStride = 348     // ClockClientClkVfPointStatusV3
	ctrlStride   = 36      // ClockClientClkVfPointControlV1
	// Field offsets (all three structs place vf_points at 0x44 or 0x68).
	maskOffset         = 0x04 // vf_points_mask: NvU32[8] = 32 bytes
	maskLen            = 32
	infoPointsOffset   = 0x44
	statusPointsOffset = 0x68
	ctrlPointsOffset   = 0x44
	// Within a control point: type_(4) + rsvd(16) + prog.freq_offset_khz.
	ctrlFreqOffsetKHz = 20
	// Within a status point: type_(4) freq_khz(4) voltage_uv(8).
	statusFreqKHz   = 4
	statusVoltageUV = 8
	// Within an info point: type_(4) b_voltage_based(1).
	infoType         = 0
	infoVoltageBased = 4
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
	gpu     GPU
	gpuName string
	inited  bool
	mask    [maskLen]byte // active-point mask from GetInfo
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
	if _, err := queryInterface(idClkVfPointsGetControl); err == nil {
		p.PreBlackwell = true
	}
	if !p.Blackwell && !p.PreBlackwell {
		return p, fmt.Errorf("nvapi: no VF interface exposed")
	}
	return p, nil
}

// Init initializes NVAPI, enumerates the first GPU, and reads the active-point
// mask. The mask is required for every status/control read and write.
func Init() (*Session, error) {
	fn, err := queryInterface(idInitialize)
	if err != nil {
		return nil, err
	}
	if ret, _ := call(fn); ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: Initialize failed: %d", ret)
	}

	fn, err = queryInterface(idEnumPhysicalGPUs)
	if err != nil {
		return nil, err
	}
	var gpus [64]uintptr
	var count int32
	if ret, _ := call(fn, uintptr(unsafe.Pointer(&gpus[0])), uintptr(unsafe.Pointer(&count))); ret != NVAPI_OK {
		return nil, fmt.Errorf("nvapi: EnumPhysicalGPUs failed: %d", ret)
	}
	if count == 0 {
		return nil, fmt.Errorf("nvapi: no GPUs found")
	}

	var name string
	if fn, err := queryInterface(idGetFullName); err == nil {
		var buf [256]byte
		if ret, _ := call(fn, gpus[0], uintptr(unsafe.Pointer(&buf[0]))); ret == NVAPI_OK {
			for i, b := range buf {
				if b == 0 {
					name = string(buf[:i])
					break
				}
			}
		}
	}

	s := &Session{gpu: GPU(gpus[0]), gpuName: name, inited: true}
	if err := s.loadMask(); err != nil {
		s.Close()
		return nil, fmt.Errorf("nvapi: read active-point mask: %w", err)
	}
	return s, nil
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

// loadMask reads the active-point mask via GetInfo and stores it on the session.
func (s *Session) loadMask() error {
	fn, err := queryInterface(idClkVfPointsGetInfo)
	if err != nil {
		return err
	}
	buf := make([]byte, infoSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, infoSize))
	if ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return fmt.Errorf("ClkVfPointsGetInfo failed: %d (%s)", ret, errorText(ret))
	}
	copy(s.mask[:], buf[maskOffset:maskOffset+maskLen])
	return nil
}

// activeIndices returns the point indices set in the active-point mask.
// The mask is 32 bytes (8 x uint32 little-endian). Bit i lives in
// word i/32, bit position i%32.
func (s *Session) activeIndices() []int {
	idx := make([]int, 0, pointCount)
	for i := 0; i < pointCount; i++ {
		word := binary.LittleEndian.Uint32(s.mask[(i/32)*4 : (i/32)*4+4])
		if word&(1<<(uint(i)%32)) != 0 {
			idx = append(idx, i)
		}
	}
	return idx
}

// ReadCurve reads the current VF curve: frequency, voltage, and offset per
// active point. Frequencies/voltages come from the status struct; offsets come
// from the control struct.
func (s *Session) ReadCurve() ([]VFPoint, error) {
	if !s.inited {
		return nil, fmt.Errorf("nvapi: not initialized")
	}

	// Read offsets from the control struct first (needed to populate OffsetKHz).
	offsets, err := s.readOffsets()
	if err != nil {
		return nil, err
	}

	fn, err := queryInterface(idClkVfPointsGetStatus)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, statusSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(3, statusSize))
	copy(buf[maskOffset:maskOffset+maskLen], s.mask[:])
	if ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return nil, fmt.Errorf("ClkVfPointsGetStatus failed: %d (%s)", ret, errorText(ret))
	}

	points := make([]VFPoint, 0, pointCount)
	for _, i := range s.activeIndices() {
		off := statusPointsOffset + i*statusStride
		freq := binary.LittleEndian.Uint32(buf[off+statusFreqKHz:])
		volt := binary.LittleEndian.Uint32(buf[off+statusVoltageUV:])
		if freq == 0 && volt == 0 {
			continue
		}
		points = append(points, VFPoint{
			Index:     i,
			FreqKHz:   freq,
			VoltageUV: volt,
			OffsetKHz: offsets[i],
		})
	}
	return points, nil
}

// readOffsets reads current per-point frequency offsets into the given slice.
func (s *Session) readOffsets() ([]int32, error) {
	fn, err := queryInterface(idClkVfPointsGetControl)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, controlSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, controlSize))
	copy(buf[maskOffset:maskOffset+maskLen], s.mask[:])
	if ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return nil, fmt.Errorf("ClkVfPointsGetControl failed: %d (%s)", ret, errorText(ret))
	}
	offsets := make([]int32, pointCount)
	for i := 0; i < pointCount; i++ {
		off := ctrlPointsOffset + i*ctrlStride + ctrlFreqOffsetKHz
		offsets[i] = int32(binary.LittleEndian.Uint32(buf[off:]))
	}
	return offsets, nil
}

// SetOffset sets the frequency offset (kHz) for a single VF point using a
// read-modify-write: it reads the current control table, changes one point's
// freq_offset_khz, and writes the whole table back. The mask is set to all
// active points (matching PenguinBurner's flow), because the driver applies the
// full table on SetControl.
func (s *Session) SetOffset(pointIndex int, offsetKHz int32) error {
	if !s.inited {
		return fmt.Errorf("nvapi: not initialized")
	}
	if pointIndex < 0 || pointIndex >= pointCount {
		return fmt.Errorf("nvapi: point index %d out of range", pointIndex)
	}

	// Read current control table.
	buf := make([]byte, controlSize)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, controlSize))
	copy(buf[maskOffset:maskOffset+maskLen], s.mask[:])

	getFn, err := queryInterface(idClkVfPointsGetControl)
	if err != nil {
		return err
	}
	if ret, _ := call(getFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return fmt.Errorf("ClkVfPointsGetControl failed: %d (%s)", ret, errorText(ret))
	}

	// Modify the target point's freq_offset_khz.
	off := ctrlPointsOffset + pointIndex*ctrlStride + ctrlFreqOffsetKHz
	binary.LittleEndian.PutUint32(buf[off:], uint32(offsetKHz))

	// Write back.
	setFn, err := queryInterface(idClkVfPointsSetControl)
	if err != nil {
		return err
	}
	if ret, _ := call(setFn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return fmt.Errorf("ClkVfPointsSetControl failed: %d (%s)", ret, errorText(ret))
	}
	return nil
}

// SetAllOffsets sets offsets for multiple points.
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

// ReadVoltage reads current GPU core voltage in microvolts.
func (s *Session) ReadVoltage() (uint32, error) {
	fn, err := queryInterface(idClientVoltRailsStatus)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 0x4C)
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(1, 0x4C))
	if ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return 0, fmt.Errorf("ClientVoltRailsGetStatus failed: %d (%s)", ret, errorText(ret))
	}
	return binary.LittleEndian.Uint32(buf[0x28:]), nil
}

// errorText looks up an NVAPI error message.
func errorText(status int32) string {
	fn, err := queryInterface(idGetErrorMessage)
	if err != nil {
		return fmt.Sprintf("status %d", status)
	}
	var buf [64]byte
	if ret, _ := call(fn, uintptr(status), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return fmt.Sprintf("status %d", status)
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf[:])
}
