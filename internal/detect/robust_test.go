package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/sim"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// TestRobustnessToUnseenConditions checks that the low false-alarm rate is not an artefact of tuning
// against one simulator setting. It sweeps honest traces over speeds, sampling intervals and
// turn/pause behaviour that were NOT used while choosing the thresholds.
func TestRobustnessToUnseenConditions(t *testing.T) {
	type cond struct {
		name string
		mode sim.Mode
		n    int
		dt   time.Duration
	}
	mk := func(name string, speed, speedVar, turn, pause, accel float64) sim.Mode {
		return sim.Mode{Name: name, SpeedMS: speed, SpeedVar: speedVar, TurnDeg: turn, PauseP: pause, MaxAccel: accel}
	}
	conds := []cond{
		{"slow stroll, 1s", mk("stroll", 0.6, 0.4, 25, 0.10, 1.0), 600, time.Second},
		{"jogging, 2s", mk("jog", 3.0, 0.15, 8, 0.01, 1.5), 300, 2 * time.Second},
		{"city bus, 1s", mk("bus", 8, 0.6, 3, 0.20, 1.5), 600, time.Second},
		{"highway, 1s", mk("highway", 30, 0.1, 1, 0.005, 2.5), 600, time.Second},
		{"highway, 5s", mk("highway", 30, 0.1, 1, 0.005, 2.5), 300, 5 * time.Second},
		{"stop-and-go traffic, 1s", mk("jam", 5, 0.8, 4, 0.25, 2.0), 600, time.Second},
		{"drive, 60s sampling", mk("sparse", 14, 0.35, 4, 0.08, 3.0), 120, time.Minute},
		{"cycling, 3s", mk("bike", 6, 0.3, 10, 0.05, 1.5), 400, 3 * time.Second},
	}
	const n = 200
	fmt.Printf("\n%-28s %9s %12s   (honest traces, %d each)\n", "condition", "flagged", "spoofed", n)
	for _, c := range conds {
		flagged, spoofed := 0, 0
		for s := int64(1000); s < 1000+n; s++ { // seeds disjoint from the ones used earlier
			switch Judge(Analyze(sim.Honest(c.mode, c.n, c.dt, s), All(DefaultConfig()))) {
			case Suspicious:
				flagged++
			case Spoofed:
				flagged++
				spoofed++
			}
		}
		fmt.Printf("%-28s %8.1f%% %11.1f%%\n", c.name, 100*float64(flagged)/n, 100*float64(spoofed)/n)
		if spoofed > 0 {
			t.Errorf("%s: %d honest traces called likely-spoofed", c.name, spoofed)
		}
		if float64(flagged)/n > 0.05 {
			t.Errorf("%s: %.1f%% of honest traces flagged (limit 5%%)", c.name, 100*float64(flagged)/n)
		}
	}
}

// Real receivers occasionally emit a single wild fix (multipath in an urban canyon, a cold-start
// position) while the user keeps moving normally. This measures what one such glitch does.
func TestSingleGlitchSpikeBehaviour(t *testing.T) {
	// Policy: ONE isolated bad fix that immediately corrects itself is what a receiver glitch looks
	// like, so it never changes the verdict by itself, whatever its size up to MaxGlitchM. It is still
	// REPORTED (low confidence) for sizes the receiver could not plausibly produce as noise. What
	// escalates the verdict is repetition (see TestSpikeDowngradeIsNotALoophole) or a size beyond
	// MaxGlitchM, which no single-fix error reaches.
	for _, tc := range []struct {
		name       string
		offsetM    float64
		wantReport bool // must produce a lone-outlier finding
		want       Verdict
	}{
		{"60 m glitch (ordinary urban multipath)", 60, false, Clean},
		{"150 m glitch", 150, false, Clean},
		{"300 m glitch", 300, true, Clean},
		{"5 km glitch (cold-start fix)", 5000, true, Clean},
		{"90 km glitch", 90_000, true, Suspicious}, // beyond receiver glitches: the teleport check says so
	} {
		tr := sim.Honest(sim.Drive, 300, time.Second, 42)
		tr.Fixes[150].Point = glitch(tr.Fixes[150].Point, tc.offsetM)
		fs := Analyze(tr, All(DefaultConfig()))
		got := Judge(fs)
		reported := false
		for _, f := range fs {
			if f.Check == "speed" && f.Confidence == Low {
				reported = true
			}
			if f.Check == "acceleration" {
				t.Errorf("%s: a lone outlier must not also be reported as an acceleration: %s", tc.name, f.Reason)
			}
		}
		fmt.Printf("%-42s -> %-10s reported=%v\n", tc.name, got, reported)
		if got != tc.want {
			t.Errorf("%s: verdict %s, want %s", tc.name, got, tc.want)
		}
		if tc.wantReport && !reported && tc.want == Clean {
			t.Errorf("%s: a glitch this large should still be mentioned", tc.name)
		}
	}
}

func rank(v Verdict) int {
	switch v {
	case Clean:
		return 0
	case Suspicious:
		return 1
	}
	return 2
}

func glitch(p geo.Point, metres float64) geo.Point { return geo.Destination(p, 90, metres) }

// The spike downgrade must not become a loophole. A single out-and-back excursion is genuinely
// indistinguishable from a glitch (and is reported, at Low), but repeated or sustained excursions
// are not glitches and must stay High.
func TestSpikeDowngradeIsNotALoophole(t *testing.T) {
	far := func(p geo.Point) geo.Point { return geo.Destination(p, 90, 400_000) } // 400 km away

	// Sustained relocation: the position leaves and STAYS away. Must be High.
	tr := sim.Honest(sim.Drive, 300, time.Second, 5)
	for i := 100; i < 300; i++ {
		tr.Fixes[i].Point = far(tr.Fixes[i].Point)
	}
	if got := Judge(Analyze(tr, All(DefaultConfig()))); got != Spoofed {
		t.Errorf("sustained relocation: got %s, want likely-spoofed", got)
	}

	// Two consecutive far fixes then back (a 2-fix excursion): not a lone outlier, must be High.
	tr = sim.Honest(sim.Drive, 300, time.Second, 5)
	tr.Fixes[100].Point = far(tr.Fixes[100].Point)
	tr.Fixes[101].Point = far(tr.Fixes[101].Point)
	if got := Judge(Analyze(tr, All(DefaultConfig()))); got != Spoofed {
		t.Errorf("2-fix excursion: got %s, want likely-spoofed", got)
	}

	// Alternating between two places over and over: many "spikes" in one trace. A real receiver does
	// not emit a wild fix every few seconds, so this must not be waved through as glitches.
	tr = sim.Honest(sim.Drive, 300, time.Second, 5)
	for i := 20; i < 280; i += 10 {
		tr.Fixes[i].Point = far(tr.Fixes[i].Point)
	}
	got := Judge(Analyze(tr, All(DefaultConfig())))
	fmt.Printf("26 lone outliers in one trace -> %s\n", got)
	if got == Clean {
		t.Errorf("26 isolated outliers in one trace must not be clean")
	}
}

// The outlier handling must not become a place for real anomalies to hide.
func TestOutlierHandlingDoesNotHideRealSpeedChanges(t *testing.T) {
	base := func() *trace.Trace { return sim.Honest(sim.Drive, 300, time.Second, 7) }

	// 1) A SUSTAINED change to an impossible speed is not an outlier (the fixes after it stay changed),
	//    so it must still be caught: a car that suddenly moves at 400 m/s for twenty seconds.
	tr := base()
	pos := tr.Fixes[100].Point
	for i := 101; i < 121; i++ {
		pos = geo.Destination(pos, 90, 400)
		tr.Fixes[i].Point = pos
	}
	for i := 121; i < 300; i++ {
		tr.Fixes[i].Point = geo.Destination(pos, 90, float64(i-120)*14) // resumes normal speed from the new place
	}
	if got := Judge(Analyze(tr, All(DefaultConfig()))); got != Spoofed {
		t.Errorf("sustained 400 m/s: got %s, want likely-spoofed", got)
	}

	// 2) Every other fix displaced (a zig-zag): not a lone outlier, because the neighbours disagree with
	//    each other's expectation. It must not be silently absorbed.
	tr = base()
	for i := 20; i < 280; i += 2 {
		tr.Fixes[i].Point = geo.Destination(tr.Fixes[i].Point, 90, 600)
	}
	if got := Judge(Analyze(tr, All(DefaultConfig()))); got == Clean {
		t.Errorf("a zig-zag of 130 displaced fixes was judged clean")
	}

	// 3) Two adjacent bad fixes are not an isolated outlier either.
	tr = base()
	tr.Fixes[150].Point = geo.Destination(tr.Fixes[150].Point, 90, 5000)
	tr.Fixes[151].Point = geo.Destination(tr.Fixes[151].Point, 90, 5000)
	if got := Judge(Analyze(tr, All(DefaultConfig()))); got == Clean {
		t.Errorf("a two-fix excursion of 5 km was judged clean")
	}
}
