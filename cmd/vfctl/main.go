// vfctl - GPU voltage/frequency curve tool for NVIDIA cards.
//
// Subcommands:
//
//	list     - list profiles in an Afterburner cfg
//	show     - dump a profile's curve
//	validate - check a curve for notches/discontinuities
//	create   - generate an undervolt curve from a stock profile
//	apply    - write a generated curve into a profile section
//	hex      - emit just the VFCurve hex (for manual paste)
//
// The Afterburner cfg path is the default profile file for the RTX 5090 FE
// on this machine; override with --cfg.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/erfianugrah/vfctl/internal/curve"
	"github.com/erfianugrah/vfctl/internal/nvapi"
	"github.com/erfianugrah/vfctl/internal/profile"
	"github.com/erfianugrah/vfctl/internal/testloop"
	"github.com/erfianugrah/vfctl/internal/vfcurve"
)

const defaultCfg = `/mnt/c/Program Files (x86)/MSI Afterburner/Profiles/VEN_10DE&DEV_2B85&SUBSYS_205710DE&REV_A1&BUS_1&DEV_0&FN_0.cfg`

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "list":
		err = cmdList(args)
	case "show":
		err = cmdShow(args)
	case "validate":
		err = cmdValidate(args)
	case "create":
		err = cmdCreate(args)
	case "apply":
		err = cmdApply(args)
	case "hex":
		err = cmdHex(args)
	case "probe":
		err = cmdProbe(args)
	case "live":
		err = cmdLive(args)
	case "set":
		err = cmdSet(args)
	case "reset":
		err = cmdReset(args)
	case "persist":
		err = cmdPersist(args)
	case "test":
		err = cmdTest(args)
	case "selftest":
		err = cmdSelfTest(args)
	case "watch":
		err = cmdWatch(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "vfctl: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `vfctl - GPU VF curve tool

Commands:
  list      [--cfg path]
  show      <Profile> [--min-mv N] [--max-mv N] [--cfg path]
  validate  <Profile> [--cfg path]
  create    --from Profile2 --voltage 900 --freq 2797 [--ramp-from 850] [--cfg path]
            Prints the generated curve; combine with apply/hex to persist.
  apply     --to Profile3 --voltage 900 --freq 2797 [--ramp-from 850] [--from Profile2] [--cfg path] [--dry-run]
            Writes the curve into the cfg. Close Afterburner first.
  hex       --voltage 900 --freq 2797 [--ramp-from 850] [--from Profile2] [--cfg path]
            Prints only the VFCurve hex for manual paste.
  probe     Report which NVAPI VF interfaces the driver exposes (Windows only)
  live      Read live VF curve from GPU via NVAPI (Windows only)
  set       --voltage 900 --freq 2797 [--ramp-from 850]
            Apply undervolt directly via NVAPI, bypassing Afterburner (Windows only)
  reset     Zero all VF point offsets (return to stock curve)
  persist   --voltage 900 --freq 2797 [--ramp-from 850] [--remove]
            Install a scheduled task that re-applies the curve at sign-in (admin)
  test      --voltage 900 --freq 2812 --game path.exe [--min-freq N] [--run-for 30m]
            Apply, launch the game, watch for TDR; step down 15 MHz on crash (admin)
  selftest  Prove the write path: +15 MHz on one point, read back, reset (admin)
  watch     Poll core clock + voltage + nearest VF point every second (load test)
`)
}

func cfgFlag(fs *flag.FlagSet) *string {
	return fs.String("cfg", defaultCfg, "path to Afterburner profile cfg")
}

func loadCurve(f *profile.File, name string) (*vfcurve.Curve, error) {
	hexBlob := f.VFCurve(name)
	if hexBlob == "" {
		return nil, fmt.Errorf("profile %s has no VFCurve", name)
	}
	return vfcurve.Parse(hexBlob)
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	cfgPath := cfgFlag(fs)
	fs.Parse(args)

	f, err := profile.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("%-18s %-10s %-7s %s\n", "Profile", "Status", "Points", "Offset range")
	fmt.Println(strings.Repeat("-", 60))
	for _, s := range f.Sections() {
		hexBlob := f.VFCurve(s)
		if hexBlob == "" {
			continue
		}
		c, err := vfcurve.Parse(hexBlob)
		if err != nil {
			fmt.Printf("%-18s parse error: %v\n", s, err)
			continue
		}
		var mn, mx float32
		for i, p := range c.Points {
			if i == 0 || p.Offset < mn {
				mn = p.Offset
			}
			if i == 0 || p.Offset > mx {
				mx = p.Offset
			}
		}
		status := "modified"
		if mn == 0 && mx == 0 {
			status = "stock"
		}
		fmt.Printf("%-18s %-10s %-7d %+.0f to %+.0f MHz\n", s, status, len(c.Points), mn, mx)
	}
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := cfgFlag(fs)
	minMV := fs.Float64("min-mv", 0, "minimum voltage to display")
	maxMV := fs.Float64("max-mv", 2000, "maximum voltage to display")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("show needs a profile name")
	}

	f, err := profile.Load(*cfgPath)
	if err != nil {
		return err
	}
	c, err := loadCurve(f, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("%6s %8s %8s %8s\n", "mV", "base", "offset", "final")
	fmt.Println(strings.Repeat("-", 34))
	for _, p := range c.Points {
		if p.Voltage < float32(*minMV) || p.Voltage > float32(*maxMV) {
			continue
		}
		marker := ""
		if p.Offset != 0 {
			marker = " *"
		}
		fmt.Printf("%6.0f %8.0f %+8.0f %8.0f%s\n", p.Voltage, p.Freq, p.Offset, p.Final(), marker)
	}
	return nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	cfgPath := cfgFlag(fs)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("validate needs a profile name")
	}

	f, err := profile.Load(*cfgPath)
	if err != nil {
		return err
	}
	c, err := loadCurve(f, fs.Arg(0))
	if err != nil {
		return err
	}
	issues := c.Validate(850, 10, 400)
	if len(issues) == 0 {
		fmt.Println("no issues")
		return nil
	}
	for _, is := range issues {
		fmt.Printf("%-15s %6.0fmV  %s\n", is.Kind, is.Voltage, is.Detail)
	}
	return fmt.Errorf("%d issue(s)", len(issues))
}

// genFlags are shared by create/apply/hex.
type genFlags struct {
	from     string
	voltage  float64
	freq     float64
	rampFrom float64
	cfgPath  string
}

func bindGenFlags(fs *flag.FlagSet, g *genFlags) {
	fs.StringVar(&g.from, "from", "Profile2", "source (stock) profile")
	fs.Float64Var(&g.voltage, "voltage", 0, "target voltage (mV)")
	fs.Float64Var(&g.freq, "freq", 0, "target frequency (MHz)")
	fs.Float64Var(&g.rampFrom, "ramp-from", 850, "ramp start voltage (mV)")
	g.cfgPath = *cfgFlag(fs)
}

func generate(g genFlags) (*vfcurve.Curve, *profile.File, error) {
	if g.voltage == 0 || g.freq == 0 {
		return nil, nil, fmt.Errorf("--voltage and --freq are required")
	}
	f, err := profile.Load(g.cfgPath)
	if err != nil {
		return nil, nil, err
	}
	stock, err := loadCurve(f, g.from)
	if err != nil {
		return nil, nil, err
	}
	// Guard: generating from a non-stock baseline compounds offsets.
	for _, p := range stock.Points {
		if p.Offset != 0 {
			return nil, nil, fmt.Errorf("source profile %s is not stock (offset %+.0f at %.0fmV); generate from a zero-offset profile", g.from, p.Offset, p.Voltage)
		}
	}
	uv, err := vfcurve.Undervolt(stock, float32(g.voltage), float32(g.freq), float32(g.rampFrom))
	if err != nil {
		return nil, nil, err
	}
	return uv, f, nil
}

func preview(uv *vfcurve.Curve, fromV, toV float32) {
	fmt.Printf("%6s %8s %8s %8s\n", "mV", "base", "offset", "final")
	fmt.Println(strings.Repeat("-", 34))
	for _, p := range uv.Points {
		if p.Voltage >= fromV && p.Voltage <= toV {
			fmt.Printf("%6.0f %8.0f %+8.0f %8.0f\n", p.Voltage, p.Freq, p.Offset, p.Final())
		}
	}
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	var g genFlags
	bindGenFlags(fs, &g)
	fs.Parse(args)

	uv, _, err := generate(g)
	if err != nil {
		return err
	}
	fmt.Printf("target %.0fMHz @ %.0fmV, ramp from %.0fmV\n\n", g.freq, g.voltage, g.rampFrom)
	preview(uv, float32(g.rampFrom)-10, float32(g.voltage)+50)
	return nil
}

func cmdHex(args []string) error {
	fs := flag.NewFlagSet("hex", flag.ExitOnError)
	var g genFlags
	bindGenFlags(fs, &g)
	fs.Parse(args)

	uv, _, err := generate(g)
	if err != nil {
		return err
	}
	fmt.Println(uv.Encode())
	return nil
}

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	var g genFlags
	bindGenFlags(fs, &g)
	to := fs.String("to", "", "target profile section (e.g. Profile3)")
	dryRun := fs.Bool("dry-run", false, "print, don't write")
	fs.Parse(args)
	if *to == "" {
		return fmt.Errorf("--to is required")
	}

	uv, f, err := generate(g)
	if err != nil {
		return err
	}
	// Validate before writing - refuse broken curves.
	if issues := uv.Validate(850, 10, 400); len(issues) > 0 {
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "  %-15s %6.0fmV  %s\n", is.Kind, is.Voltage, is.Detail)
		}
		return fmt.Errorf("generated curve failed validation, refusing to write")
	}
	if *dryRun {
		fmt.Printf("would write %.0fMHz @ %.0fmV to %s\n\n", g.freq, g.voltage, *to)
		preview(uv, float32(g.rampFrom)-10, float32(g.voltage)+50)
		return nil
	}
	if err := f.SetVFCurve(*to, uv.Encode()); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}
	fmt.Printf("wrote %.0fMHz @ %.0fmV to %s\n", g.freq, g.voltage, *to)
	fmt.Println("restart Afterburner and load the profile")
	return nil
}

// keep sort import used (reserved for future sorted output)
var _ = sort.Strings

// --- NVAPI direct commands (Windows only) ---

func cmdProbe(args []string) error {
	probe, err := nvapi.ProbeInterfaces()
	if err != nil {
		return err
	}
	fmt.Printf("Blackwell (ClkVfPoints): %v\n", probe.Blackwell)
	fmt.Printf("Pre-Blackwell (ClockBoostTable): %v\n", probe.PreBlackwell)
	return nil
}

func cmdLive(args []string) error {
	fs := flag.NewFlagSet("live", flag.ExitOnError)
	minMV := fs.Float64("min-mv", 0, "minimum voltage to display")
	maxMV := fs.Float64("max-mv", 2000, "maximum voltage to display")
	fs.Parse(args)

	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()

	fmt.Printf("GPU: %s\n", sess.GPUName())

	volt, err := sess.ReadVoltage()
	if err == nil {
		fmt.Printf("Current voltage: %.0f mV\n", float64(volt)/1000)
	}
	clk, err := sess.ReadClocks()
	if err == nil {
		fmt.Printf("Core clock: %.0f MHz | Memory: %.0f MHz\n", float64(clk.CoreKHz)/1000, float64(clk.MemoryKHz)/1000)
	}

	points, err := sess.ReadCurve()
	if err != nil {
		return err
	}

	fmt.Printf("\n%4s %8s %8s %8s\n", "idx", "MHz", "mV", "offset")
	fmt.Println(strings.Repeat("-", 32))
	for _, p := range points {
		mv := float64(p.VoltageUV) / 1000
		if mv < *minMV || mv > *maxMV {
			continue
		}
		marker := ""
		if p.OffsetKHz != 0 {
			marker = " *"
		}
		fmt.Printf("%4d %8.0f %8.0f %+8.0f%s\n", p.Index, float64(p.FreqKHz)/1000, mv, float64(p.OffsetKHz)/1000, marker)
	}
	return nil
}

func cmdSet(args []string) error {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	voltage := fs.Float64("voltage", 0, "target voltage (mV)")
	freq := fs.Float64("freq", 0, "target frequency (MHz)")
	rampFrom := fs.Float64("ramp-from", 850, "ramp start voltage (mV)")
	dryRun := fs.Bool("dry-run", false, "print, don't write")
	fs.Parse(args)

	if *voltage == 0 || *freq == 0 {
		return fmt.Errorf("--voltage and --freq are required")
	}

	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()

	fmt.Printf("GPU: %s\n", sess.GPUName())

	// Read live curve
	points, err := sess.ReadCurve()
	if err != nil {
		return err
	}

	stockAtTarget, ok := curve.StockAt(points, *voltage)
	if !ok {
		return fmt.Errorf("no VF point at %.0fmV", *voltage)
	}

	fmt.Printf("Live stock @ %.0fmV: %.0f MHz\n", *voltage, stockAtTarget)
	fmt.Printf("Target: %.0f MHz (offset %+.0f MHz)\n", *freq, *freq-stockAtTarget)

	offsets, err := curve.BuildOffsets(points, *voltage, *freq, *rampFrom)
	if err != nil {
		return err
	}

	if *dryRun {
		fmt.Printf("\nWould set %d points (dry run)\n", len(offsets))
		for _, p := range points {
			if off, ok := offsets[p.Index]; ok {
				mv := float64(p.VoltageUV) / 1000
				fmt.Printf("  %4d: %.0fmV %.0fMHz -> %+.0f MHz\n", p.Index, mv, float64(p.FreqKHz)/1000, float64(off)/1000)
			}
		}
		return nil
	}

	fmt.Printf("\nApplying %d offsets...\n", len(offsets))
	if err := sess.SetAllOffsets(offsets); err != nil {
		return err
	}
	if err := curve.VerifyOffsets(sess, offsets); err != nil {
		return fmt.Errorf("curve did not fully apply (offsets are volatile; re-run set or reset to stock): %w", err)
	}
	fmt.Println("Done")
	return nil
}

func cmdReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print, don't write")
	fs.Parse(args)

	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()

	fmt.Printf("GPU: %s\n", sess.GPUName())

	// Read current curve to see which points have non-zero offsets
	points, err := sess.ReadCurve()
	if err != nil {
		return err
	}

	// Build reset map for non-zero offsets
	resets := make(map[int]int32)
	for _, p := range points {
		if p.OffsetKHz != 0 {
			resets[p.Index] = 0
		}
	}

	if len(resets) == 0 {
		fmt.Println("All offsets already zero (stock)")
		return nil
	}

	if *dryRun {
		fmt.Printf("Would reset %d points to zero (dry run)\n", len(resets))
		for _, p := range points {
			if p.OffsetKHz != 0 {
				fmt.Printf("  %4d: %.0fmV %+.0f MHz -> 0\n", p.Index, float64(p.VoltageUV)/1000, float64(p.OffsetKHz)/1000)
			}
		}
		return nil
	}

	fmt.Printf("Resetting %d points to zero...\n", len(resets))
	if err := sess.SetAllOffsets(resets); err != nil {
		return err
	}
	if err := curve.VerifyOffsets(sess, resets); err != nil {
		return fmt.Errorf("reset did not fully apply (reboot to return to stock, offsets are volatile): %w", err)
	}
	fmt.Println("Done - back to stock curve")
	return nil
}

// cmdPersist installs (or removes) a scheduled task that re-applies the curve
// at sign-in. NVAPI offsets are volatile - they live in driver memory and are
// lost on reboot or driver reset. A logon task re-runs `vfctl set`, which reads
// the live curve, so it is temperature-correct at every boot.
func cmdPersist(args []string) error {
	fs := flag.NewFlagSet("persist", flag.ExitOnError)
	voltage := fs.Float64("voltage", 0, "target voltage (mV)")
	freq := fs.Float64("freq", 0, "target frequency (MHz)")
	rampFrom := fs.Float64("ramp-from", 850, "ramp start voltage (mV)")
	remove := fs.Bool("remove", false, "remove the scheduled task")
	fs.Parse(args)

	const taskName = "vfctl-undervolt"

	if *remove {
		out, err := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").CombinedOutput()
		if err != nil {
			return fmt.Errorf("schtasks delete: %v: %s", err, out)
		}
		fmt.Printf("removed scheduled task %s\n", taskName)
		return nil
	}

	if *voltage == 0 || *freq == 0 {
		return fmt.Errorf("--voltage and --freq are required (or --remove)")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	tr := fmt.Sprintf(`"%s" set --voltage %.0f --freq %.0f --ramp-from %.0f`, exe, *voltage, *freq, *rampFrom)

	// /RL HIGHEST runs elevated; /SC ONLOGON fires at sign-in.
	out, err := exec.Command("schtasks", "/Create", "/TN", taskName, "/SC", "ONLOGON",
		"/RL", "HIGHEST", "/TR", tr, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create: %v: %s", err, out)
	}
	fmt.Printf("installed %s -> %s\n", taskName, tr)
	fmt.Println("curve re-applied at every sign-in (elevated)")
	return nil
}

// countTDR returns the number of TDR events (Event ID 4101, nvlddmkm) in the
// System log. Returns -1 on error.
func countTDR() (int, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		`(Get-WinEvent -FilterHashtable @{LogName='System'; Id=4101} -ErrorAction SilentlyContinue | Measure-Object).Count`).Output()
	if err != nil {
		return -1, err
	}
	return testloop.ParseTDRCount(string(out))
}

// cmdSelfTest proves the NVAPI write path end-to-end on the live card without
// touching the operating curve: it writes +15 MHz (one step) to a single
// mid-range point, reads it back, then restores the original offset. This is
// the one-shot gate for "can we ditch Afterburner" - if it passes, the write
// path and struct layout are correct; if it fails, do not trust `set`.
func cmdSelfTest(args []string) error {
	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()
	fmt.Printf("GPU: %s\n", sess.GPUName())

	points, err := sess.ReadCurve()
	if err != nil {
		return err
	}

	// Pick a mid-range point (nearest 900 mV) as the probe.
	var probe nvapi.VFPoint
	best := 1e9
	for _, p := range points {
		mv := float64(p.VoltageUV) / 1000
		if d := curve.AbsF(mv - 900); d < best {
			best = d
			probe = p
		}
	}
	if probe.Index == 0 && probe.VoltageUV == 0 {
		return fmt.Errorf("no probe point found")
	}

	orig := probe.OffsetKHz
	fmt.Printf("probe point %d @ %.0f mV: current offset %+d kHz\n", probe.Index, float64(probe.VoltageUV)/1000, orig)
	fmt.Printf("writing +15 MHz (one step) to that point only...\n")

	if err := sess.SetOffset(probe.Index, orig+15000); err != nil {
		return fmt.Errorf("single-point write failed (write path is NOT verified): %w", err)
	}

	after, err := sess.ReadCurve()
	if err != nil {
		return fmt.Errorf("read-back failed: %w", err)
	}
	var got int32
	found := false
	for _, p := range after {
		if p.Index == probe.Index {
			got = p.OffsetKHz
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("probe point %d missing from read-back", probe.Index)
	}
	if int64(got)-int64(orig) != 15000 {
		return fmt.Errorf("read-back mismatch: wrote %+d kHz, read %+d kHz", orig+15000, got)
	}

	fmt.Printf("read-back confirmed: %+d kHz (was %+d)\n", got, orig)
	fmt.Printf("restoring original offset...\n")
	if err := sess.SetOffset(probe.Index, orig); err != nil {
		return fmt.Errorf("restore failed (reboot returns to stock; offsets are volatile): %w", err)
	}

	fmt.Println("SELFTEST PASSED: write path is verified. `set` is safe to use.")
	return nil
}

// cmdWatch polls core clock, voltage, and the nearest VF point on an interval
// and logs every sample to a CSV file (plus stdout). This is the load-test
// telemetry: run it while a game/benchmark loads the GPU and confirm the card
// reaches target clock at target voltage - and capture the full time series
// (clock, voltage, spikes, read errors) for later analysis with mlr/duckdb.
func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	interval := fs.Duration("interval", time.Second, "poll interval")
	csvPath := fs.String("csv", "", "write CSV samples to this file (default: stdout only)")
	fs.Parse(args)

	sess, err := nvapi.Init()
	if err != nil {
		return err
	}
	defer sess.Close()

	// CSV writer (append-safe: truncate + header on a fresh open).
	var w *csv.Writer
	var fh *os.File
	if *csvPath != "" {
		fh, err = os.OpenFile(*csvPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open csv: %w", err)
		}
		defer fh.Close()
		w = csv.NewWriter(fh)
		if err := w.Write([]string{
			"unix_ns", "elapsed", "core_mhz", "mem_mhz", "volt_mv",
			"point_mv", "offset_mhz", "read_error",
		}); err != nil {
			return err
		}
		w.Flush()
	}

	fmt.Printf("GPU: %s\n", sess.GPUName())
	if *csvPath != "" {
		fmt.Printf("logging to %s\n", *csvPath)
	}
	fmt.Printf("%8s %9s %9s %8s %9s\n", "elapsed", "core MHz", "volt mV", "point mV", "offset MHz")
	fmt.Println(strings.Repeat("-", 48))

	start := time.Now()
	for {
		sample := time.Now()
		clk, clkErr := sess.ReadClocks()
		volt, voltErr := sess.ReadVoltage()

		// Nearest VF point to current voltage, for the applied offset.
		var nearest nvapi.VFPoint
		best := 1e9
		points, _ := sess.ReadCurve()
		for _, p := range points {
			d := float64(p.VoltageUV)/1000 - float64(volt)/1000
			if d < 0 {
				d = -d
			}
			if d < best {
				best = d
				nearest = p
			}
		}

		readErr := ""
		if clkErr != nil {
			readErr = clkErr.Error()
		} else if voltErr != nil {
			readErr = voltErr.Error()
		}

		// Console line.
		if clkErr != nil {
			fmt.Printf("%8s %s\n", time.Since(start).Round(time.Second), clkErr)
		} else {
			fmt.Printf("%8s %9.0f %9.0f %8.0f %+9.0f\n",
				time.Since(start).Round(time.Second),
				float64(clk.CoreKHz)/1000,
				float64(volt)/1000,
				float64(nearest.VoltageUV)/1000,
				float64(nearest.OffsetKHz)/1000)
		}

		// CSV row.
		if w != nil {
			row := []string{
				strconv.FormatInt(sample.UnixNano(), 10),
				strconv.FormatFloat(time.Since(start).Seconds(), 'f', 3, 64),
				strconv.FormatFloat(float64(clk.CoreKHz)/1000, 'f', 1, 64),
				strconv.FormatFloat(float64(clk.MemoryKHz)/1000, 'f', 1, 64),
				strconv.FormatFloat(float64(volt)/1000, 'f', 1, 64),
				strconv.FormatFloat(float64(nearest.VoltageUV)/1000, 'f', 0, 64),
				strconv.FormatFloat(float64(nearest.OffsetKHz)/1000, 'f', 1, 64),
				readErr,
			}
			if err := w.Write(row); err != nil {
				return err
			}
			w.Flush()
		}

		time.Sleep(*interval)
	}
}

// a TDR (Event 4101) or process crash, and step down 15 MHz on failure.
func cmdTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	voltage := fs.Float64("voltage", 0, "target voltage (mV)")
	freq := fs.Float64("freq", 0, "starting frequency (MHz)")
	minFreq := fs.Float64("min-freq", 0, "stop stepping down below this (MHz)")
	game := fs.String("game", "", "path to the benchmark/game executable")
	gameArgs := fs.String("game-args", "", "arguments for the game (space-separated)")
	runFor := fs.Duration("run-for", 30*time.Minute, "clean run required before declaring stable")
	maxSteps := fs.Int("max-steps", 10, "max step-down attempts (15 MHz each)")
	rampFrom := fs.Float64("ramp-from", 850, "ramp start voltage (mV)")
	fs.Parse(args)

	if *voltage == 0 || *freq == 0 || *game == "" {
		return fmt.Errorf("--voltage, --freq, and --game are required")
	}
	if *minFreq == 0 {
		*minFreq = testloop.MinFreqFloor(*freq, *maxSteps)
	}

	for step := 0; ; step++ {
		f := testloop.StepFreq(*freq, step)
		if f < *minFreq {
			return fmt.Errorf("stepped down to %.0f MHz (below --min-freq) with no stable result", f)
		}
		fmt.Printf("\n=== attempt %d: %.0f MHz @ %.0f mV ===\n", step, f, *voltage)
		if err := curve.ApplyCurve(*voltage, f, *rampFrom); err != nil {
			return err
		}

		baseline, err := countTDR()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot read TDR event log: %v (crash detection disabled)\n", err)
		}

		cmd := exec.Command(*game, strings.Fields(*gameArgs)...)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launch %s: %w", *game, err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		deadline := time.NewTimer(*runFor)
		tick := time.NewTicker(2 * time.Second)

		var outcome string // "stable" | "exit-clean" | "exit-crash" | "tdr"
		for outcome == "" {
			select {
			case err := <-done:
				if err == nil {
					outcome = "exit-clean"
				} else {
					outcome = "exit-crash"
				}
			case <-deadline.C:
				outcome = "stable"
			case <-tick.C:
				if n, e := countTDR(); e == nil && baseline >= 0 && n > baseline {
					cmd.Process.Kill()
					outcome = "tdr"
				}
			}
		}
		tick.Stop()
		deadline.Stop()

		switch outcome {
		case "stable":
			fmt.Printf("\nSTABLE: %.0f MHz @ %.0f mV survived %s with no TDR\n", f, *voltage, *runFor)
			return nil
		case "exit-clean":
			fmt.Printf("clean exit at %.0f MHz @ %.0f mV\n", f, *voltage)
			return nil
		case "tdr", "exit-crash":
			fmt.Printf("%s at %.0f MHz - stepping down 15 MHz\n", outcome, f)
		}
	}
}
