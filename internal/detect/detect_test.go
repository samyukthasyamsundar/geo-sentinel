package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/sim"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func fx(sec float64, lat, lon float64, acc float64, line int) trace.Fix {
	f := trace.Fix{Point: geo.Point{Lat: lat, Lon: lon}, Time: t0.Add(time.Duration(sec * float64(time.Second))), AccuracyM: acc, Line: line}
	return f
}

func has(fs []Finding, check string, c Confidence) bool {
	for _, f := range fs {
		if f.Check == check && f.Confidence == c {
			return true
		}
	}
	return false
}

func TestSpeedFlagsImpossibleSpeed(t *testing.T) {
	// 1 degree of latitude (~111 km) in 60 s = 1850 m/s.
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 5, 2), fx(60, 11, 10, 5, 3)}}
	fs := Speed{DefaultConfig()}.Run(tr)
	if !has(fs, "speed", High) {
		t.Fatalf("want a high speed finding, got %+v", fs)
	}
	if !strings.Contains(fs[0].Reason, "km/h") {
		t.Errorf("reason should state the speed: %q", fs[0].Reason)
	}
}

func TestSpeedIgnoresNormalMovementAndNoise(t *testing.T) {
	// 100 m in 10 s = 10 m/s: a car in town. Fine.
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 5, 2), fx(10, 10.0009, 10, 5, 3)}}
	if fs := (Speed{DefaultConfig()}).Run(tr); len(fs) != 0 {
		t.Errorf("normal driving flagged: %+v", fs)
	}
	// Two fixes 1 s apart that differ by 150 m with 100 m accuracy each: pure noise, not a speeding car.
	noisy := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 100, 2), fx(1, 10.00135, 10, 100, 3)}}
	if fs := (Speed{DefaultConfig()}).Run(noisy); len(fs) != 0 {
		t.Errorf("reported accuracy should excuse noise: %+v", fs)
	}
}

func TestSpeedCannotBeExcusedByAbsurdAccuracy(t *testing.T) {
	// A forger claiming 1,000,000 m accuracy to excuse a huge jump: the forgiveness is capped at 200 m.
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 1e6, 2), fx(10, 11, 10, 1e6, 3)}}
	if !has(Speed{DefaultConfig()}.Run(tr), "speed", High) {
		t.Error("huge claimed inaccuracy must not hide a 111 km jump")
	}
}

func TestSpeedSkipsTinyIntervals(t *testing.T) {
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 0, 2), fx(0.1, 10.0001, 10, 0, 3)}}
	if fs := (Speed{DefaultConfig()}).Run(tr); len(fs) != 0 {
		t.Errorf("sub-500ms interval should be skipped: %+v", fs)
	}
}

func TestTeleportCatchesForgedTimestamps(t *testing.T) {
	// 5000 km jump that the file claims took 10 hours = 139 m/s: faster than the limit, so High.
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 47.6, -122.3, 5, 2), fx(36000, 48.85, 2.35, 5, 3)}}
	fs := Teleport{DefaultConfig()}.Run(tr)
	if !has(fs, "teleport", High) {
		t.Fatalf("got %+v", fs)
	}
	// The same distance over 4 days is slow enough to be a flight: only Medium, and say why.
	slow := &trace.Trace{Fixes: []trace.Fix{fx(0, 47.6, -122.3, 5, 2), fx(4*86400, 48.85, 2.35, 5, 3)}}
	fs = Teleport{DefaultConfig()}.Run(slow)
	if !has(fs, "teleport", Medium) || !strings.Contains(fs[0].Reason, "gap") {
		t.Fatalf("got %+v", fs)
	}
}

func TestTeleportIgnoresShortHops(t *testing.T) {
	tr := &trace.Trace{Fixes: []trace.Fix{fx(0, 10, 10, 5, 2), fx(600, 10.2, 10, 5, 3)}} // 22 km
	if fs := (Teleport{DefaultConfig()}).Run(tr); len(fs) != 0 {
		t.Errorf("%+v", fs)
	}
}

func TestOrderingFindsBackwardsAndConflictingTimes(t *testing.T) {
	tr := &trace.Trace{Fixes: []trace.Fix{
		fx(10, 10, 10, 5, 2), fx(5, 10, 10, 5, 3), // goes backwards
		fx(20, 10, 10, 5, 4), fx(20, 10.01, 10, 5, 5), // same time, 1 km apart
	}}
	fs := Ordering{}.Run(tr)
	if len(fs) != 2 {
		t.Fatalf("want 2 findings, got %+v", fs)
	}
	if !strings.Contains(fs[0].Reason, "backwards") || !strings.Contains(fs[1].Reason, "share the timestamp") {
		t.Errorf("%+v", fs)
	}
	// An exact duplicate row is harmless and must not be flagged.
	dup := &trace.Trace{Fixes: []trace.Fix{fx(1, 10, 10, 5, 2), fx(1, 10, 10, 5, 3)}}
	if fs := (Ordering{}).Run(dup); len(fs) != 0 {
		t.Errorf("exact duplicate flagged: %+v", fs)
	}
}

func TestAccelFlagsSuddenSpeedChange(t *testing.T) {
	// 0 m/s to ~200 m/s within seconds, at speeds that rule out jitter.
	tr := &trace.Trace{Fixes: []trace.Fix{
		fx(0, 10, 10, 0, 2), fx(10, 10, 10.0001, 0, 3), fx(20, 10, 10.0002, 0, 4),
		fx(30, 10.02, 10.0002, 0, 5), // 2.2 km in 10 s
	}}
	if !has(Accel{DefaultConfig()}.Run(tr), "acceleration", Medium) {
		t.Fatal("want an acceleration finding")
	}
}

func TestAccelIgnoresWalkingJitter(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		tr := sim.Honest(sim.Walk, 200, time.Second, seed)
		if fs := (Accel{DefaultConfig()}).Run(tr); len(fs) != 0 {
			t.Fatalf("seed %d: walking jitter flagged: %+v", seed, fs[0])
		}
	}
}

func TestAccuracyChecksNeedEnoughData(t *testing.T) {
	var fixes []trace.Fix
	for i := 0; i < 5; i++ {
		fixes = append(fixes, fx(float64(i), 10, 10, 0.1, i+2))
	}
	if fs := (Accuracy{}).Run(&trace.Trace{Fixes: fixes}); len(fs) != 0 {
		t.Errorf("5 points are too few to judge: %+v", fs)
	}
}

func TestAccuracyFlagsPerfectAndConstant(t *testing.T) {
	var fixes []trace.Fix
	for i := 0; i < 30; i++ {
		fixes = append(fixes, fx(float64(i), 10, 10, 0.5, i+2))
	}
	fs := Accuracy{}.Run(&trace.Trace{Fixes: fixes})
	if !has(fs, "accuracy", Medium) || !has(fs, "accuracy", Low) {
		t.Fatalf("want medium (too perfect) and low (constant): %+v", fs)
	}
	for _, f := range fs {
		if f.Confidence == High {
			t.Error("accuracy is a weak signal and must never be High")
		}
	}
}

func TestAccuracyAcceptsVaryingRealisticValues(t *testing.T) {
	tr := sim.Honest(sim.Walk, 100, time.Second, 7)
	if fs := (Accuracy{}).Run(tr); len(fs) != 0 {
		t.Errorf("%+v", fs)
	}
}

func TestSmoothnessFlagsPerfectStraightConstantMotion(t *testing.T) {
	tr := sim.Linear(geo.Point{Lat: 47.6, Lon: -122.3}, geo.Point{Lat: 47.7, Lon: -122.1}, 14, time.Second, 5)
	fs := Smoothness{}.Run(tr)
	if !has(fs, "smoothness", Medium) || !has(fs, "smoothness", Low) {
		t.Fatalf("want speed (medium) and heading (low) findings: %+v", fs)
	}
}

func TestSmoothnessFlagsFrozenPosition(t *testing.T) {
	tr := sim.Static(60, time.Second, geo.Point{Lat: 47.6, Lon: -122.3}, 5)
	if !has(Smoothness{}.Run(tr), "smoothness", Medium) {
		t.Fatal("identical coordinates for 60 fixes must be flagged")
	}
}

func TestJudge(t *testing.T) {
	mk := func(cs ...Confidence) []Finding {
		var fs []Finding
		for _, c := range cs {
			fs = append(fs, Finding{Confidence: c})
		}
		return fs
	}
	cases := []struct {
		name string
		fs   []Finding
		want Verdict
	}{
		{"none", nil, Clean},
		{"two low", mk(Low, Low), Clean},
		{"three low", mk(Low, Low, Low), Suspicious},
		{"one medium", mk(Medium), Suspicious},
		{"one high", mk(Low, High), Spoofed},
	}
	for _, c := range cases {
		if got := Judge(c.fs); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestAnalyzeSortsOutOfOrderInputButStillReportsIt(t *testing.T) {
	tr := &trace.Trace{Fixes: []trace.Fix{fx(10, 10, 10, 5, 2), fx(0, 10, 10, 5, 3), fx(20, 10, 10, 5, 4)}}
	fs := Analyze(tr, All(DefaultConfig()))
	if !has(fs, "ordering", Medium) {
		t.Errorf("out-of-order input should be reported: %+v", fs)
	}
	if has(fs, "speed", High) {
		t.Error("after sorting, the fixes are stationary: no speed finding")
	}
}

func TestAnalyzeEmptyAndNil(t *testing.T) {
	if Analyze(nil, All(DefaultConfig())) != nil || Analyze(&trace.Trace{}, All(DefaultConfig())) != nil {
		t.Error("nothing to analyse should give nothing")
	}
}
