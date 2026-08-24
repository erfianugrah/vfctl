# AGENTS.md - vfctl

## Purpose

CLI to generate, validate, and apply NVIDIA GPU voltage/frequency curves without fighting MSI Afterburner's curve editor. Built after Afterburner's editor repeatedly produced broken curves (notch artifacts, cliff discontinuities, temperature-stale baselines) on an RTX 5090 FE.

## Stack

- Go 1.24+ (module `github.com/erfianugrah/vfctl`), stdlib only - no dependencies
- Single static binary; cross-compiles `GOOS=windows go build`
- See `~/.pi/agent/skills/software-architecture/SKILL.md` for the bounded-context layout rationale

## Layout

```
cmd/vfctl/main.go         CLI: list / show / validate / create / apply / hex / probe / live / set / reset
internal/vfcurve/         VFCurve blob parse/encode, undervolt generation, validation
internal/profile/         Afterburner profile .cfg read/write (INI-like)
internal/nvapi/           Windows-only syscall layer over nvapi64.dll
testdata/                 Real curves captured from the 5090 FE (stock, broken)
```

## Key facts (verified, do not re-learn)

- **VFCurve blob**: 12-byte header (magic u32 `0x00020000`, count u32 = 127, flags u32), then N x 3 float32 LE (voltage mV, freq MHz, offset MHz), zero-padded to 3224 bytes. The Python prototype used an 888-char header - wrong, regression-tested.
- The stored `freq` in a saved profile is the FINAL curve at save time, not vBIOS stock (Unwinder, Guru3D). Only the offset is real. Generate only from a zero-offset (stock) profile - `generate()` refuses otherwise.
- The base curve shifts with temperature (GPU Boost). Offsets computed against a cold baseline display differently when warm - inherent to the interface, affects Afterburner too.
- NVIDIA frequency steps: 15 MHz.
- RTX 5090 (Blackwell) exposes the `ClockClientClkVfPoints*` NVAPI family; pre-Blackwell uses `ClockBoostTable`. Probe both.
- **NVAPI write requires Administrator privileges** (error -137 = `NVAPI_INVALID_USER_PRIVILEGE`).
- **NVAPI read works without admin** (probe, live, read voltage).
- Fan: manual fan % clamps to 30% minimum on NVIDIA; 0 RPM only via vBIOS auto mode. No fan control in this tool - FanControl handles it as well as the hardware allows.

## NVAPI structs (from PenguinBurner hidden_nvapi_vf.py, verified via ctypes)

Three structs, all using the SAME 255-point index space and the active-point
mask read from GetInfo (0x507B4B59). Do not mix generations: the status is v3
(255 pts), the control is v1 (255 pts), the info is v1 (255 pts).

```
InfoV1 (0x507B4B59) - active mask + point types:
  size 0x182C (6188)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 24 bytes at 0x44
    +0x00: type (u32)
    +0x04: b_voltage_based (u8)

StatusV3 (0x21537AD4) - freq/voltage, version 3:
  size 0x15B0C (88844)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 348 bytes at 0x68
    +0x00: type (u32)
    +0x04: freq_kHz (u32)
    +0x08: voltage_uV (u32)
    +0x0C: vf_tuple_base, +0x34: vf_tuple_offset

ControlV1 (0x23F1B133 read / 0x0733E009 write) - offsets:
  size 0x2420 (9248)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 36 bytes at 0x44
    +0x00: type (u32)
    +0x04: rsvd[16]
    +0x14: prog.freq_offset_kHz (i32)   <-- 0x44 + i*36 + 20
```

Write flow is READ-MODIFY-WRITE: GetControl fills type_/rsvd for all points,
then change one freq_offset_kHz, then SetControl writes the whole table back
with the full active mask. A fresh zeroed buffer does NOT stick (driver ignores
writes to points whose type_ doesn't match what it returned).

The earlier 0x48 base and 128-point/28-byte layout came from Loong0x00's
read-only PoC, which reused the status stride for the control read and never
exercised a write - the 4-byte error only surfaced once selftest actually
attempted a write.

## Commands

```bash
go build ./...                      # build
go test ./...                       # tests (fixtures in testdata/)
GOOS=windows go build -o vfctl.exe ./cmd/vfctl

# Afterburner cfg mode (Linux/WSL)
./vfctl list                        # profiles in the cfg
./vfctl show Profile2 --min-mv 880
./vfctl validate Profile1           # notch/discontinuity check
./vfctl create --voltage 900 --freq 2797 --ramp-from 850     # preview
./vfctl apply --to Profile5 --voltage 900 --freq 2797 --dry-run

# NVAPI direct (Windows)
./vfctl.exe probe                   # check available APIs (no admin)
./vfctl.exe live                    # read live curve (no admin)
./vfctl.exe set --voltage 900 --freq 2797  # apply undervolt (needs admin)
./vfctl.exe reset                   # zero all offsets (needs admin)
```

## Conventions

- Default `--cfg` is the 5090 FE profile file path (WSL mount). Override for other machines.
- `apply` refuses to write a curve that fails validation.
- Afterburner must be closed before `apply` (it overwrites the cfg from memory on exit).
- `set` reads the live curve before applying - offsets are temperature-correct.

## Roadmap

See TODO.md. The NVAPI direct path is now primary; Afterburner cfg support stays for import/backup.
