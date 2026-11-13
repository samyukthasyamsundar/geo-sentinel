package detect

import (
	"fmt"
	"math"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// ---- Ordering: timestamps that go backwards or repeat ----

// Ordering flags a source file whose timestamps are not strictly increasing. A recording device
// writes fixes in time order; a file assembled by hand or by a script often does not.
type Ordering struct{}

func (Ordering) Name() string { return "ordering" }

func (Ordering) Run(t *trace.Trace) []Finding {
	var out []Finding
	for i := 1; i < len(t.Fixes); i++ {
		prev, cur := t.Fixes[i-1], t.Fixes[i]
		switch {
		case cur.Time.Before(prev.Time):
			out = append(out, newFinding("ordering", Medium, cur,
				"timestamp goes backwards by %s (line %d precedes line %d in time)",
				prev.Time.Sub(cur.Time).Round(time.Millisecond), cur.Line, prev.Line))
		case cur.Time.Equal(prev.Time) && geo.DistanceM(prev.Point, cur.Point) > 1:
			out = append(out, newFinding("ordering", Medium, cur,
				"two different positions (%.0f m apart) share the timestamp %s",
				geo.DistanceM(prev.Point, cur.Point), cur.Time.Format(time.RFC3339Nano)))
		}
	}
	return out
}

// ---- Speed: faster than any plausible vehicle ----

// Speed flags intervals whose implied speed exceeds MaxSpeedMS.
//
// An isolated outlier is treated differently from a relocation. If fix i is far from both its
// neighbours but those neighbours are close to EACH OTHER, the receiver emitted one wild reading
// (multipath, a cold-start fix) and the device carried on where it was. Both legs of that spike are
// "too fast", but they are one event and not proof of fabrication, so it is reported once, at Low
// confidence. A relocation, where the position jumps and STAYS, is still High.
type Speed struct{ Cfg Config }

func (Speed) Name() string { return "speed" }

func (s Speed) Run(t *trace.Trace) []Finding {
	var out []Finding
	n := len(t.Fixes)
	tooFast := make([]bool, n) // tooFast[i]: the step from fix i-1 to fix i is implausibly fast
	speeds := make([]float64, n)
	dists := make([]float64, n)
	gaps := make([]time.Duration, n)
	for i := 1; i < n; i++ {
		prev, cur := t.Fixes[i-1], t.Fixes[i]
		dt := cur.Time.Sub(prev.Time)
		gaps[i] = dt
		if dt < s.Cfg.MinGapForSpeed {
			continue
		}
		d := geo.DistanceM(prev.Point, cur.Point)
		dists[i] = d
		// Allow for the reported measurement error: two fixes with 50 m accuracy each can differ by
		// 100 m without any movement. Subtracting it avoids flagging noisy but honest data.
		eff := math.Max(0, d-noise(prev)-noise(cur))
		speeds[i] = eff / dt.Seconds()
		tooFast[i] = speeds[i] > s.Cfg.MaxSpeedMS
	}

	for i := 1; i < n; i++ {
		if !tooFast[i] {
			continue
		}
		// Part of an isolated spike? Fix i-1 jumped away from fix i-2 and fix i came back: i.e. the
		// previous step was also too fast, and fix i is close to fix i-2.
		if i >= 2 && i == spikeReturn(t, tooFast, i, s.Cfg) {
			continue // the return leg is folded into the spike reported at its start
		}
		if i+1 < n && isSpikeStart(t, tooFast, i, s.Cfg) {
			out = append(out, newFinding("speed", Low, t.Fixes[i],
				"fix at line %d is a lone outlier: it jumps %.0f m away and the very next fix returns to the earlier track, so this looks like one bad GPS reading (%.0f m/s implied), not a relocation",
				t.Fixes[i].Line, dists[i], speeds[i]))
			continue
		}
		dt := gaps[i]
		out = append(out, newFinding("speed", High, t.Fixes[i],
			"implied speed %s exceeds the plausible maximum of %.0f km/h: %s in %s",
			speedText(speeds[i]), s.Cfg.MaxSpeedMS*3.6, distText(dists[i]), dt.Round(time.Millisecond)))
	}
	return out
}

// isSpikeStart reports whether step i (into fix i) is the outbound leg of a one-fix spike: the next
// step is also too fast, and fix i+1 is back near fix i-1.
func isSpikeStart(t *trace.Trace, tooFast []bool, i int, cfg Config) bool {
	if i+1 >= len(t.Fixes) || !tooFast[i+1] {
		return false
	}
	return returnsNear(t, i-1, i+1, cfg)
}

// spikeReturn returns i if step i is the return leg of a spike that began at step i-1, else -1.
func spikeReturn(t *trace.Trace, tooFast []bool, i int, cfg Config) int {
	if i >= 2 && tooFast[i-1] && returnsNear(t, i-2, i, cfg) {
		return i
	}
	return -1
}

// returnsNear reports whether fix b is close to fix a, closer than the track could have moved at
// the limit speed over the elapsed time (plus noise): the two fixes are consistent with the device
// simply having stayed on its path.
func returnsNear(t *trace.Trace, a, b int, cfg Config) bool {
	if a < 0 || b >= len(t.Fixes) {
		return false
	}
	fa, fb := t.Fixes[a], t.Fixes[b]
	dt := fb.Time.Sub(fa.Time).Seconds()
	if dt <= 0 {
		return false
	}
	allowed := cfg.MaxSpeedMS*dt + noise(fa) + noise(fb)
	return geo.DistanceM(fa.Point, fb.Point) <= allowed
}

// speedText formats a speed for people. Beyond the speed of sound, extra digits are meaningless, so
// it says "impossible" and gives the order of magnitude instead.
func speedText(ms float64) string {
	if ms > 1000 { // about 3,600 km/h: beyond the fastest aircraft, so the figure itself means nothing
		return "far beyond any vehicle (over 3,600 km/h)"
	}
	return fmt.Sprintf("%.0f km/h", ms*3.6)
}

func distText(m float64) string {
	if m >= 10_000 {
		return fmt.Sprintf("%.0f km", m/1000)
	}
	return fmt.Sprintf("%.0f m", m)
}

// noise is the position uncertainty to forgive for a fix: its reported accuracy, capped so that a
// device claiming absurd inaccuracy cannot excuse any jump.
func noise(f trace.Fix) float64 {
	if !f.HasAccuracy() {
		return 0
	}
	return math.Min(f.AccuracyM, 200)
}

// ---- Teleport: a jump no time stamp can excuse ----

// Teleport flags single jumps over TeleportM, however the timestamps read. It exists because Speed
// can be defeated by also forging the timestamps; a 5,000 km jump is suspicious even if the file
// claims it took many hours, when the surrounding fixes show nobody was travelling.
//
// It shares Speed's treatment of lone outliers, so the two checks never disagree about the same
// event: a jump out that the very next fix undoes is one bad reading. Receivers do produce those,
// but not enormous ones, so a lone outlier is Medium (never High) up to MaxGlitchM and High beyond
// it, a distance no real single-fix error reaches.
type Teleport struct{ Cfg Config }

// MaxGlitchM is the largest single-fix error treated as a plausible receiver glitch.
const MaxGlitchM = 200_000

func (Teleport) Name() string { return "teleport" }

func (tp Teleport) Run(t *trace.Trace) []Finding {
	var out []Finding
	n := len(t.Fixes)
	jump := make([]bool, n) // jump[i]: the step into fix i is a teleport-sized jump
	for i := 1; i < n; i++ {
		jump[i] = geo.DistanceM(t.Fixes[i-1].Point, t.Fixes[i].Point) >= tp.Cfg.TeleportM
	}
	for i := 1; i < n; i++ {
		if !jump[i] {
			continue
		}
		prev, cur := t.Fixes[i-1], t.Fixes[i]
		d := geo.DistanceM(prev.Point, cur.Point)

		// Return leg of a lone outlier: reported with the outbound leg, so skip it here.
		if i >= 2 && jump[i-1] && returnsNear(t, i-2, i, tp.Cfg) {
			continue
		}
		dt := cur.Time.Sub(prev.Time)
		c := Medium
		note := ""
		lone := i+1 < n && jump[i+1] && returnsNear(t, i-1, i+1, tp.Cfg)
		switch {
		case lone && d <= MaxGlitchM:
			note = ", but the next fix returns to the earlier track, so it looks like a single bad reading"
		case lone:
			c = High
			note = fmt.Sprintf(", and although the next fix returns, a single-fix error of %.0f km is beyond any receiver glitch", d/1000)
		case dt <= 0 || d/dt.Seconds() > tp.Cfg.MaxSpeedMS:
			c = High
			note = ", faster than any plausible vehicle"
		case dt > tp.Cfg.MaxGap:
			note = fmt.Sprintf(" after a %s gap with no fixes in between", dt.Round(time.Second))
		}
		out = append(out, newFinding("teleport", c, cur, "position jumps %.0f km in one step%s", d/1000, note))
	}
	return out
}

// ---- isolated outliers ----

// isolatedOutliers marks fixes that are wrong on their own: far from where BOTH neighbours say the
// device was, while those neighbours agree with each other. A single bad reading from the receiver
// (multipath, a cold-start fix) looks like this; a relocation does not, because the fixes after it
// stay at the new place and so do not agree with the ones before.
//
// "Far" is relative to the noise the device itself reported, with a floor, so ordinary jitter on a
// noisy trace is never marked.
func isolatedOutliers(t *trace.Trace, cfg Config) []bool {
	n := len(t.Fixes)
	out := make([]bool, n)
	for i := 1; i+1 < n; i++ {
		a, b, c := t.Fixes[i-1], t.Fixes[i], t.Fixes[i+1]
		if b.Time.Sub(a.Time) < cfg.MinGapForSpeed || c.Time.Sub(b.Time) < cfg.MinGapForSpeed {
			continue
		}
		if !returnsNear(t, i-1, i+1, cfg) {
			continue // the neighbours disagree, so this is not a spike on an otherwise steady track
		}
		// How far b sits from the straight line a->c at b's moment in time.
		span := c.Time.Sub(a.Time).Seconds()
		frac := b.Time.Sub(a.Time).Seconds() / span
		expectLat := a.Lat + (c.Lat-a.Lat)*frac
		expectLon := a.Lon + (c.Lon-a.Lon)*frac
		off := geo.DistanceM(b.Point, geo.Point{Lat: expectLat, Lon: expectLon})
		// A real vehicle can swerve, so allow a generous lateral deviation before calling it an outlier:
		// the reported noise of all three fixes, or 50 m, whichever is larger.
		slack := math.Max(50, noise(a)+noise(b)+noise(c))
		out[i] = off > slack
	}
	return out
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

// maxExcusedOutliers is how many isolated outliers a trace of n fixes may have before they stop being
// treated as receiver glitches: 1% of the fixes, and at least 2 so a short trace is not penalised for a
// single bad reading.
func maxExcusedOutliers(n int) int {
	if m := n / 100; m > 2 {
		return m
	}
	return 2
}

// ---- Accel: speed changes no vehicle can make ----

// Accel flags implausibly sudden changes of speed. A spoofer who moves a point at constant speed
// to a distant place and back produces a speed step that real inertia does not allow.
//
// Differencing two noisy positions to get a speed, then differencing two speeds to get an
// acceleration, amplifies noise badly at short intervals: a 15 m position error over 1 s looks like
// 15 m/s of speed. So the check only flags a change that the REPORTED accuracy cannot explain. For
// each pair of adjacent intervals it computes the largest speed change the noise could account for
// and subtracts it before comparing with the plausible limit.
type Accel struct{ Cfg Config }

func (Accel) Name() string { return "acceleration" }

func (a Accel) Run(t *trace.Trace) []Finding {
	var out []Finding
	type seg struct {
		v     float64 // raw implied speed
		slack float64 // speed error the position noise could produce, in m/s
		mid   time.Time
	}
	var segs []seg
	var idx []int
	// One bad fix corrupts the speed into it and the speed out of it, and both then look like an
	// acceleration. So build the speeds from the fixes that are NOT isolated outliers; the outlier is
	// reported by the speed check, which is where it belongs.
	bad := isolatedOutliers(t, a.Cfg)
	// Excusing outliers is only safe when there are few of them. A receiver glitches rarely; a trace in
	// which a sizeable share of fixes look like "isolated outliers" is not a trace with glitches, it is
	// one where the positions have been tampered with, and quietly discarding them would hide that.
	// Past the limit, stop excusing any of them and let the speed changes stand.
	if countTrue(bad) > maxExcusedOutliers(len(t.Fixes)) {
		bad = make([]bool, len(t.Fixes))
	}
	var keep []int
	for i := range t.Fixes {
		if !bad[i] {
			keep = append(keep, i)
		}
	}
	for k := 1; k < len(keep); k++ {
		i := keep[k]
		prev, cur := t.Fixes[keep[k-1]], t.Fixes[i]
		dt := cur.Time.Sub(prev.Time)
		if dt < a.Cfg.MinGapForSpeed || dt > a.Cfg.MaxGap {
			continue
		}
		// Without a reported accuracy we cannot excuse anything, which is the safe direction for a
		// "no noise model" input but makes the check noisy; assume a typical consumer 10 m.
		n := noise(prev) + noise(cur)
		if !prev.HasAccuracy() || !cur.HasAccuracy() {
			n = 20
		}
		d := geo.DistanceM(prev.Point, cur.Point)
		segs = append(segs, seg{v: d / dt.Seconds(), slack: n / dt.Seconds(), mid: prev.Time.Add(dt / 2)})
		idx = append(idx, i)
	}
	for i := 1; i < len(segs); i++ {
		dt := segs[i].mid.Sub(segs[i-1].mid).Seconds()
		if dt <= 0 {
			continue
		}
		// The speed change that survives after granting both segments their full noise allowance.
		change := math.Abs(segs[i].v-segs[i-1].v) - segs[i].slack - segs[i-1].slack
		if change <= 0 {
			continue
		}
		acc := change / dt
		if acc > a.Cfg.MaxAccelMS2 {
			out = append(out, newFinding("acceleration", Medium, t.Fixes[idx[i]],
				"speed changes from %.1f to %.1f m/s in %.1f s: even after allowing for the reported position error that is %.1f m/s^2, more than the plausible %.0f m/s^2",
				segs[i-1].v, segs[i].v, dt, acc, a.Cfg.MaxAccelMS2))
		}
	}
	return out
}
