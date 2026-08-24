# Full Afterburner Coverage Plan

Status: **core VF curve + telemetry complete and verified.** This plan covers
the remaining Afterburner sliders. Each feature follows the same cycle that
worked for the curve: research interface -> implement read -> implement write
-> selftest on hardware -> verify under load -> commit.

## Current state (all verified on RTX 5090 FE, driver 610.88)

| Control | Implemented | Verified |
|---------|-------------|----------|
| Core VF curve (undervolt) | yes | yes (selftest + full gaming session) |
| Core clock read | yes | yes |
| Memory clock read | yes | yes (15801 MHz GDDR7) |
| Voltage read | yes | yes |
| Utilization read | yes | yes |
| Temperature read | yes | yes |
| Power read (live draw) | yes | yes (ClientPowerTopologyGetStatus) |
| Curve persistence | yes | yes (sign-in task) |
| Memory clock offset | **no** | - |
| Power limit % | **no** (read-only via perf-status flags) | - |
| Temp limit C | **no** | - |
| Core voltage offset | **no** | - |
| Overvoltage +100mV | **no** | - |
| Fan curve | out of scope | NVIDIA clamps manual 30% min |

## The one lesson that governs this whole plan

From the curve work: **the documented-looking path may be wrong, and the
correct path may be one name away from a wrong one.**
`ClientPowerPoliciesGetStatus` vs `ClientPowerTopologyGetStatus` - one word,
completely different data (policy vs live draw). Every interface below gets
the same treatment: verify against source, test the read first, then the
write, then on hardware.

## The Pstates20 path (documented, preferred for clock offsets)

`NvAPI_GPU_GetPstates20` / `SetPstates20` (0x6FF81213 / 0x0F5FA652) is the
**documented public API** for per-clock-domain offsets. This is the preferred
path for memory clock offset - no reverse-engineering needed.

Struct chain (from docs.nvidia.com/nvapi, Release 590):

```
NV_GPU_PSTATES20_INFO_V2:
  version: u32
  flags: u32
  numPstates: u32
  numClocks: u32
  numBaseVoltages: u32
  pstates[16]:
    pstateId: NV_GPU_PERF_PSTATE_ID
    flags: u32 (bit 0: bIsEditable)
    clocks[32]: NV_GPU_PSTATE20_CLOCK_ENTRY_V1
      domainId: NV_GPU_PUBLIC_CLOCK_ID (GRAPHICS=0, MEMORY=4)
      typeId: NV_GPU_PERF_PSTATE20_CLOCK_TYPE_ID
      flags: u32 (bit 0: bIsEditable - "overclockable")
      freqDelta_kHz: NV_GPU_PERF_PSTATES20_PARAM_DELTA
        value: NvS32 (current delta)
        valueRange: { min NvS32, max NvS32 } (valid delta range!)
      data: union { single.freq_kHz | range.{minFreq,maxFreq,domainId,minV,maxV} }
    baseVoltages[16]: NV_GPU_PSTATE20_BASE_VOLTAGE_ENTRY_V1
  ov: (overvolt section)

NV_GPU_PSTATE20_BASE_VOLTAGE_ENTRY_V1:
  domainId: NV_GPU_PERF_VOLTAGE_INFO_DOMAIN_ID (CORE=0)
  flags: u32
  voltDelta_uV: NV_GPU_PERF_PSTATES20_PARAM_DELTA
    value: NvS32 (voltage offset in uV!)
    valueRange: { min, max }
```

Key properties:
- `freqDelta_kHz.valueRange` gives the **driver-reported valid range** for each
  domain's offset - free validation, no guessing limits
- `bIsEditable` flags mark which pstates/domains are overclockable
- Read-modify-write: GetPstates20, modify the delta, SetPstates20
- Covers **both** memory offset AND core voltage offset (voltDelta_uV) AND
  core clock offset (for non-curve use) in one interface

## Feature plans

### 1. Memory clock offset (highest value: +200-300 MHz, independent perf axis)

**Interface**: Pstates20, pstate P0, clocks[MEMORY=4].freqDelta_kHz.

**Why Pstates20 and not the VF curve**: the VF curve structs only carry core
domain points. Memory offset lives in the pstate tables.

**Design**:
```
vfctl mem --offset +200        # +200 MHz memory offset
vfctl mem --reset               # zero the memory delta
```

**Steps**:
1. Read Pstates20, dump P0 clocks + freqDelta ranges (read-only, no admin
   needed for read) - confirm MEMORY domain present, editable, and its range
2. Implement `SetPstates20` write: read-modify-write the full struct
   (same pattern as the curve - the driver may reject partial tables)
3. Selftest: write min(range, 0, +15 MHz) delta, read back, restore
4. Stability: memory OC shows as artifacts/crashes quickly; run Heaven +
   a memory-stress test at each step

**Verification gate**: `watch` shows mem_mhz climbing +200 (15801 -> 16001)
under load.

**Risk**: low. Memory offsets don't interact with the core undervolt. Worst
case is visual corruption that's instantly obvious.

### 2. Power limit % (temps/noise control; the 76W finding makes this optional)

**Interface**: `ClientPowerPoliciesSetStatus` (0xAD95F5ED) - the write
counterpart of the policy API. Read limits via `ClientPowerPoliciesGetInfo`
(0x34206D86), which returns min/def/max entries in NV_GPU_POWER_INFO_ENTRY.

**Also needed**: `NvAPI_GPU_PerfPoliciesGetStatus` (from nvapi-sys:
NV_GPU_PERF_STATUS_V1, 0x550 bytes) exposes `limits` flags -
POWER_LIMIT=1, THERMAL_LIMIT=2, VOLTAGE_REL=4, VOLTAGE_OP=8, NO_LOAD=16 -
which is the "why is the card throttled" answer, and pairs naturally with
watch telemetry.

**Design**:
```
vfctl power --limit 80          # cap at 80% of max
vfctl power --info               # show min/def/max + current policy
vfctl watch --limits             # add perf-limit flags to telemetry
```

**Value check first**: the card draws 76W avg / 103W max in The Finals. A
power limit adds nothing here. It only matters for memory-offset stability
testing (feature 1 pushes power up) or future heavier games. **Build after
memory offset if at all.**

**Risk**: low. It's a limit, not an overclock. Instantly reversible.

### 3. Core clock offset (flat offset, non-curve)

**Interface**: Pstates20, pstate P0, clocks[GRAPHICS=0].freqDelta_kHz.

**Why**: the VF curve is the better tool for undervolting, but a flat core
offset is what Afterburner's "Core Clock +" slider does. For parity.

**Design**:
```
vfctl core --offset +135        # flat +135 MHz on top of everything
vfctl core --reset
```

**Note**: this composes with the VF curve (offset + curve). Whether the
driver stacks or ignores one is an empirical question - selftest will reveal.

### 4. Core voltage offset (uV offset, non-curve)

**Interface**: Pstates20, pstate P0, baseVoltages[CORE=0].voltDelta_uV.

**Why**: redundant with the curve for undervolting, but it's Afterburner's
"Core Voltage" slider. The Pstates20 struct exposes the valid uV range
(voltDelta_uV.valueRange) so bounds are driver-provided.

**Risk**: medium - voltage writes are the sharpest tool. The valueRange cap
should keep it within safe bounds (the driver won't accept beyond its own
reported range).

### 5. Temperature limit

**Interface**: `NvAPI_GPU_SetThermalLimit` - thermal policies family
(nvapi-sys: thermal_limit / set_thermal_limit). Struct from
NV_GPU_THERMAL_LIMIT / NV_GPU_THERMAL_INFO.

**Value check**: card maxes at 61C. The temp limit never engages. **Skip
unless a future workload needs it.**

### 6. Overvoltage (+100 mV)

**Interface**: Pstates20 `ov` (overvolt) section - present in the struct but
requires the driver's voltage-unlock policy. The "extended MSI unlock" is a
driver hook that Afterburner trips via a registry/service mechanism, not a
pure NVAPI call.

**Verdict**: skip. Highest risk, zero value for the stated goals.

## Selftest gate per feature

Each feature ships a selftest variant mirroring the curve selftest:
```
vfctl selftest-mem    # +15 MHz memory delta, read back, restore
vfctl selftest-power  # set min-policy delta, read back, restore
```
A feature does not ship until its selftest passes on the card.

## Sequencing recommendation

1. **Pstates20 reader** (read-only, no risk): dump pstates/clocks/deltas/
   ranges. This single step tells us everything about features 1, 3, 4 -
   whether domains are editable, what ranges the driver allows, and whether
   the ov section is populated. One implementation, three features scouted.
2. Memory offset write + selftest (the perf win)
3. Core offset + voltage offset (parity, cheap once Pstates20 works)
4. Power limit (only if memory OC pushes power into territory that needs it)
5. Perf-limit flags in watch (nice-to-have diagnostic)
