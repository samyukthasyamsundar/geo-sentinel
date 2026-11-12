// geosentinel checks a GPS trace for signs of spoofing.
//
//	geosentinel [flags] trace.csv|trace.gpx
//
// Exit status: 0 clean, 1 suspicious, 2 likely spoofed, 3 the file could not be read, 64 bad usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/detect"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

const (
	exitClean      = 0
	exitSuspicious = 1
	exitSpoofed    = 2
	exitBadInput   = 3
	exitUsage      = 64
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("geosentinel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := detect.DefaultConfig()
	var (
		asJSON   = fs.Bool("json", false, "print machine-readable JSON")
		format   = fs.String("format", "", "input format: csv or gpx (default: from the file extension)")
		maxKMH   = fs.Float64("max-speed-kmh", cfg.MaxSpeedMS*3.6, "fastest speed treated as plausible, in km/h (raise for trains and aircraft)")
		maxShown = fs.Int("max-findings", 50, "most findings to print in text mode (0 = all)")
	)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: geosentinel [flags] trace.csv|trace.gpx")
		fmt.Fprintln(stderr, "\nChecks a GPS trace for signs that it was fabricated. Exit status: 0 clean, 1 suspicious,")
		fmt.Fprintln(stderr, "2 likely spoofed, 3 unreadable input, 64 bad usage.")
		fmt.Fprintln(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitClean
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}
	// NaN compares false with everything, so a plain range check lets it through; reject it explicitly
	// (a NaN limit would silently disable the speed check).
	if math.IsNaN(*maxKMH) || math.IsInf(*maxKMH, 0) || *maxKMH <= 0 || *maxKMH > 50_000 {
		fmt.Fprintln(stderr, "error: --max-speed-kmh must be between 0 and 50000")
		return exitUsage
	}
	cfg.MaxSpeedMS = *maxKMH / 3.6

	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitBadInput
	}
	defer f.Close()

	kind := strings.ToLower(*format)
	if kind == "" {
		kind = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
	var tr *trace.Trace
	switch kind {
	case "csv":
		tr, err = trace.ParseCSV(f)
	case "gpx":
		tr, err = trace.ParseGPX(f)
	default:
		fmt.Fprintf(stderr, "error: cannot tell the format of %q: use a .csv or .gpx extension, or --format\n", path)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %s: %v\n", path, err)
		return exitBadInput
	}
	if problems := tr.Validate(); len(problems) > 0 {
		fmt.Fprintf(stderr, "error: %s: %d invalid fix(es); first: %v\n", path, len(problems), problems[0])
		return exitBadInput
	}

	findings := detect.Analyze(tr, detect.All(cfg))
	verdict := detect.Judge(findings)

	if *asJSON {
		out := struct {
			File     string           `json:"file"`
			Fixes    int              `json:"fixes"`
			Verdict  detect.Verdict   `json:"verdict"`
			Findings []detect.Finding `json:"findings"`
		}{path, len(tr.Fixes), verdict, findings}
		if out.Findings == nil {
			out.Findings = []detect.Finding{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		printText(stdout, path, tr, findings, verdict, *maxShown)
	}
	switch verdict {
	case detect.Spoofed:
		return exitSpoofed
	case detect.Suspicious:
		return exitSuspicious
	}
	return exitClean
}

func printText(w io.Writer, path string, tr *trace.Trace, fs []detect.Finding, v detect.Verdict, limit int) {
	first, last := tr.Fixes[0].Time, tr.Fixes[len(tr.Fixes)-1].Time
	fmt.Fprintf(w, "%s: %d fixes, %s to %s\n", path, len(tr.Fixes), first.Format(time.RFC3339), last.Format(time.RFC3339))
	fmt.Fprintf(w, "verdict: %s\n", strings.ToUpper(string(v)))
	if len(fs) == 0 {
		fmt.Fprintln(w, "no findings.")
		return
	}
	fmt.Fprintf(w, "%d finding(s):\n", len(fs))
	for i, f := range fs {
		if limit > 0 && i >= limit {
			fmt.Fprintf(w, "  ... and %d more (use --max-findings 0 to show all)\n", len(fs)-limit)
			break
		}
		fmt.Fprintf(w, "  [%-6s] %-12s line %-6d %s  %s\n", f.Level, f.Check, f.Line, f.At.Format("15:04:05"), f.Reason)
	}
}
