# AGENTS.md - vfctl

## Purpose

CLI to generate, validate, and apply NVIDIA GPU voltage/frequency curves without fighting MSI Afterburner's curve editor. Built after Afterburner's editor repeatedly produced broken curves (notch artifacts, cliff discontinuities, temperature-stale baselines) on an RTX 5090 FE.

## Stack

- Go 1.24+ (module `github.com/erfianugrah/vfctl`), stdlib only - no dependencies
- Single static binary; cross-compiles `GOOS=windows go build`
- See `~/.pi/agent/skills/software-architecture/SKILL.md` for the bounded-context layout rationale

## Layout

```
cmd/vfctl/main.go         CLI: list / show / validate / create / apply / hex
internal/vfcurve/         VFCurve blob parse/encode, undervolt generation, validation
internal/profile/         Afterburner profile .cfg read/write (INI-like)
internal/nvapi/           Windows-only syscall layer over nvapi64.dll (probe stub)
testdata/                 Real curves captured from the 5090 FE (stock, broken)
```

## Key facts (verified, do not re-learn)

- VFCurve blob: 12-byte header (magic u32 `0x00020000`, count u32 = 127, flags u32), then N x 3 float32 LE (voltage mV, freq MHz, offset MHz), zero-padded to 3224 bytes. The Python prototype used an 888-char header - wrong, regression-tested.
- The stored `freq` in a saved profile is the FINAL curve at save time, not vBIOS stock (Unwinder, Guru3D). Only the offset is real. Generate only from a zero-offset (stock) profile - `generate()` refuses otherwise.
- The base curve shifts with temperature (GPU Boost). Offsets computed against a cold baseline display differently when warm - inherent to the interface, affects Afterburner too.
- NVIDIA frequency steps: 15 MHz.
- RTX 5090 (Blackwell) exposes the `ClockClientClkVfPoints*` NVAPI family; pre-Blackwell uses `ClockBoostTable`. Probe both.
- **NVAPI write requires Administrator privileges** (error -137 = `NVAPI_INVALID_USER_PRIVILEGE`).
- Fan: manual fan % clamps to 30% minimum on NVIDIA; 0 RPM only via vBIOS auto mode. No fan control in this tool - FanControl handles it as well as the hardware allows.

## Commands

```bash
go build ./...                      # build
go test ./...                       # tests (fixtures in testdata/)
GOOS=windows go build -o vfctl.exe ./cmd/vfctl

./vfctl list                        # profiles in the cfg
./vfctl show Profile2 --min-mv 880
./vfctl validate Profile1           # notch/discontinuity check
./vfctl create --voltage 900 --freq 2797 --ramp-from 850     # preview
./vfctl apply --to Profile5 --voltage 900 --freq 2797 --dry-run

# NVAPI direct (Windows, requires Administrator for write)
./vfctl.exe probe                   # check available APIs
./vfctl.exe live                    # read live curve from GPU
./vfctl.exe set --voltage 900 --freq 2797  # apply undervolt directly
```

## Conventions

- Default `--cfg` is the 5090 FE profile file path (WSL mount). Override for other machines.
- `apply` refuses to write a curve that fails validation.
- Afterburner must be closed before `apply` (it overwrites the cfg from memory on exit).

## Roadmap

See TODO.md. The endgame is NVAPI-direct apply (no Afterburner in the loop): probe -> read live base curve -> compute offsets -> SetControl -> verify read-back. Afterburner cfg support stays for import/backup only.
