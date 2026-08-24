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
	// Within a status point: type_(4) freq_khz(4) voltage_uv(8), then
	// vf_tuple_base (freq@12, volt@16) and vf_tuple_offset (freq@52, volt@56).
	// The BASE frequency we need for offset math is vf_tuple_base.freq_khz at
	// +12; freq_khz at +4 is the NOMINAL (base + applied offset) frequency.
	statusFreqKHz     = 4 // nominal (base + offset), display only
	statusVoltageUV   = 8
	statusBaseFreqKHz = 12 // vf_tuple_base.freq_khz - the true vBIOS base
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
	core    []int         // editable core VF point indices
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
	Index      int
	FreqKHz    uint32 // vBIOS base frequency (offset math uses this)
	NominalKHz uint32 // nominal = base + applied offset (display)
	VoltageUV  uint32
	OffsetKHz  int32
}

// makeVersion packs version and size.
func makeVersion(ver, size uint32) uint32 {
	return (ver << 16) | (size & 0xFFFF)
}

// loadMask reads the active-point mask AND the per-point type/voltage-based
// flags via GetInfo. Only core VF points (type==0 && voltage_based==1) are
// editable; the buffer also carries other clock domains (memory, etc.) whose
// offsets we must never touch.
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

	// Record which indices are editable core points.
	s.core = nil
	for i := 0; i < pointCount; i++ {
		off := infoPointsOffset + i*infoStride
		typ := binary.LittleEndian.Uint32(buf[off+infoType:])
		vb := buf[off+infoVoltageBased]
		if typ == 0 && vb == 1 {
			s.core = append(s.core, i)
		}
	}
	return nil
}

// coreIndices returns the editable core VF point indices (type==0,
// voltage_based==1). Falls back to the raw active mask if the info flags
// were not populated (older drivers).
func (s *Session) coreIndices() []int {
	if len(s.core) > 0 {
		return s.core
	}
	return s.activeIndices()
}

// isCore reports whether a point index is an editable core VF point.
func (s *Session) isCore(pointIndex int) bool {
	for _, i := range s.coreIndices() {
		if i == pointIndex {
			return true
		}
	}
	return false
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
	for _, i := range s.coreIndices() {
		off := statusPointsOffset + i*statusStride
		freq := binary.LittleEndian.Uint32(buf[off+statusFreqKHz:])
		base := binary.LittleEndian.Uint32(buf[off+statusBaseFreqKHz:])
		volt := binary.LittleEndian.Uint32(buf[off+statusVoltageUV:])
		if base == 0 && freq == 0 && volt == 0 {
			continue
		}
		// Fall back to nominal if the base tuple is not populated (some
		// drivers leave vf_tuple_base zero).
		if base == 0 {
			base = freq
		}
		points = append(points, VFPoint{
			Index:      i,
			FreqKHz:    base,
			NominalKHz: freq,
			VoltageUV:  volt,
			OffsetKHz:  offsets[i],
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
	if !s.isCore(pointIndex) {
		return fmt.Errorf("nvapi: point %d is not an editable core VF point (type!=0 or not voltage-based); refusing to write", pointIndex)
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

// CurrentClocks holds the current core and memory clock frequencies.
type CurrentClocks struct {
	CoreKHz   uint32
	MemoryKHz uint32
}

// ReadClocks reads the current core (graphics) and memory clock frequencies
// via NvAPI_GPU_GetAllClockFrequencies (0xDCB616C3). This is a DOCUMENTED
// public NVAPI (docs.nvidia.com/nvapi, Release 590), not reverse-engineered.
//
// Struct NV_GPU_CLOCK_FREQUENCIES_V2:
//
//	version   u32 @ 0x00  (MAKE_NVAPI_VERSION(V2, 2))
//	ClockType u32 @ 0x04  (0 = CURRENT_FREQ)
//	domain[32] struct { bIsPresent:1, reserved:31 u32; frequency u32 } @ 0x08
//	GRAPHICS = domain[0], MEMORY = domain[4]
func (s *Session) ReadClocks() (CurrentClocks, error) {
	const (
		idGetAllClockFrequencies = 0xDCB616C3
		clockTypeCurrent         = 0
		domainGraphics           = 0
		domainMemory             = 4
		clockFreqDomainSize      = 8 // u32 flags + u32 frequency
	)
	fn, err := queryInterface(idGetAllClockFrequencies)
	if err != nil {
		return CurrentClocks{}, err
	}
	buf := make([]byte, 0x108) // 8 + 32*8
	binary.LittleEndian.PutUint32(buf[0:], makeVersion(2, 0x108))
	binary.LittleEndian.PutUint32(buf[4:], clockTypeCurrent)
	if ret, _ := call(fn, uintptr(s.gpu), uintptr(unsafe.Pointer(&buf[0]))); ret != NVAPI_OK {
		return CurrentClocks{}, fmt.Errorf("GetAllClockFrequencies failed: %d (%s)", ret, errorText(ret))
	}
	freq := func(domain int) uint32 {
		return binary.LittleEndian.Uint32(buf[8+domain*clockFreqDomainSize+4:])
	}
	return CurrentClocks{CoreKHz: freq(domainGraphics), MemoryKHz: freq(domainMemory)}, nil
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
