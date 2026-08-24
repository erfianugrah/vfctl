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

## NVAPI structs (from PenguinBurner hidden_nvapi_vf.py)

```
ClkVfPointsStatusV1 (read):
  version: u32 = (1 << 16) | 0x1C28
  mask: u8[16] at 0x04 (all 0xFF)
  numClocks: u32 at 0x14 = 15
  points: 128 x 28 bytes at 0x48
    +0x00: freq_kHz (u32)
    +0x04: voltage_uV (u32)
    +0x08: reserved[20]

ClkVfPointsControlV1 (write):
  version: u32 = (1 << 16) | 0x2420
  vf_points_mask: u32[8] at 0x04 (single bit set)
  rsvd: u8[32] at 0x24
  points: 255 x 36 bytes at 0x48
    +0x00: type (u32)
    +0x04: rsvd[16]
    +0x14: prog.freq_offset_kHz (i32)
    +0x18: rsvd[16]
```

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
