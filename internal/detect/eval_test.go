package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/sim"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// This file measures the detector instead of just unit-testing it. It generates many traces of each
// kind and counts verdicts. The assertions below are deliberately loose bounds on the measured
// rates; the exact numbers are printed by `go test -run Evaluation -v` and recorded in the README.

type tally struct{ clean, suspicious, spoofed int }

func (t tally) total() int { return t.clean + t.suspicious + t.spoofed }
func (t tally) rate(n int) float64 {
	if t.total() == 0 {
		return 0
	}
	return float64(n) / float64(t.total())
}

func run(n int, gen func(seed int64) *trace.Trace) tally {
	var t tally
	cfg := DefaultConfig()
	for s := int64(1); s <= int64(n); s++ {
		switch Judge(Analyze(gen(s), All(cfg))) {
		case Clean:
			t.clean++
		case Suspicious:
			t.suspicious++
		case Spoofed:
			t.spoofed++
		}
	}
	return t
}

var paris = geo.Point{Lat: 48.8566, Lon: 2.3522}

func TestEvaluation(t *testing.T) {
	const n = 300
	type row struct {
		name   string
		gen    func(seed int64) *trace.Trace
		honest bool
	}
	rows := []row{
		{"honest: walking, 1s", func(s int64) *trace.Trace { return sim.Honest(sim.Walk, 600, time.Second, s) }, true},
		{"honest: cycling, 1s", func(s int64) *trace.Trace { return sim.Honest(sim.Bike, 600, time.Second, s) }, true},
		{"honest: driving, 1s", func(s int64) *trace.Trace { return sim.Honest(sim.Drive, 600, time.Second, s) }, true},
		{"honest: driving, 10s", func(s int64) *trace.Trace { return sim.Honest(sim.Drive, 360, 10*time.Second, s) }, true},
		{"honest: walking, 30s", func(s int64) *trace.Trace { return sim.Honest(sim.Walk, 120, 30*time.Second, s) }, true},
		{"fake: teleport to Paris", func(s int64) *trace.Trace {
			return sim.Teleport(sim.Honest(sim.Drive, 600, time.Second, s), paris)
		}, false},
		{"fake: teleport + forged clock", func(s int64) *trace.Trace {
			return sim.RelocatedWithClockEdits(sim.Honest(sim.Drive, 600, time.Second, s), paris, 10*time.Hour)
		}, false},
		{"fake: frozen mock location", func(s int64) *trace.Trace {
			return sim.Static(600, time.Second, geo.Point{Lat: 47.6, Lon: -122.3}, 5)
		}, false},
		{"fake: scripted straight drive", func(s int64) *trace.Trace {
			return sim.Linear(geo.Point{Lat: 47.6, Lon: -122.3}, geo.Point{Lat: 47.7, Lon: -122.0}, 14, time.Second, 5)
		}, false},
		{"fake: careful (honest motion, wrong place)", func(s int64) *trace.Trace {
			return sim.Careful(sim.Drive, 600, time.Second, s, paris)
		}, false},
	}

	fmt.Printf("\n%-44s %7s %11s %9s\n", "scenario ("+fmt.Sprint(n)+" traces each)", "clean", "suspicious", "spoofed")
	results := map[string]tally{}
	for _, r := range rows {
		res := run(n, r.gen)
		results[r.name] = res
		fmt.Printf("%-44s %6.1f%% %10.1f%% %8.1f%%\n", r.name,
			100*res.rate(res.clean), 100*res.rate(res.suspicious), 100*res.rate(res.spoofed))
	}

	// ---- assertions: honest traces must almost never be accused; obvious fakes must be caught ----
	for _, r := range rows {
		res := results[r.name]
		if r.honest {
			if fp := res.rate(res.spoofed); fp > 0.01 {
				t.Errorf("%s: %.1f%% of honest traces were called likely-spoofed (limit 1%%)", r.name, 100*fp)
			}
			if fp := res.rate(res.suspicious + res.spoofed); fp > 0.05 {
				t.Errorf("%s: %.1f%% of honest traces were flagged at all (limit 5%%)", r.name, 100*fp)
			}
		}
	}
	for _, name := range []string{"fake: teleport to Paris", "fake: teleport + forged clock"} {
		if d := results[name].rate(results[name].spoofed); d < 0.99 {
			t.Errorf("%s: only %.1f%% caught as likely-spoofed (want >= 99%%)", name, 100*d)
		}
	}
	for _, name := range []string{"fake: frozen mock location", "fake: scripted straight drive"} {
		res := results[name]
		if d := res.rate(res.suspicious + res.spoofed); d < 0.99 {
			t.Errorf("%s: only %.1f%% flagged (want >= 99%%)", name, 100*d)
		}
	}
	// Documented limitation, asserted so it cannot silently change: a careful relocation of honest
	// motion is NOT detectable from the trace alone.
	c := results["fake: careful (honest motion, wrong place)"]
	fmt.Printf("\nlimitation: the careful fake is flagged %.1f%% of the time; the check below pins that it is low.\n",
		100*c.rate(c.suspicious+c.spoofed))
	if c.rate(c.spoofed) > 0.05 {
		t.Errorf("careful fake called spoofed %.1f%%: a trace-only detector should not be able to tell", 100*c.rate(c.spoofed))
	}
}
