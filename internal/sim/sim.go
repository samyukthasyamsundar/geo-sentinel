// Package sim generates synthetic GPS traces: honest ones that mimic real receiver behaviour, and
// spoofed ones of varying sophistication. It exists so the detectors can be measured (how often do
// they catch each kind of fake, and how often do they wrongly flag an honest trace?) instead of
// only being unit-tested on hand-made inputs. All generators are deterministic for a given seed.
package sim

import (
	"math"
	"math/rand"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// Mode is how the honest traveller moves.
type Mode struct {
	Name     string
	SpeedMS  float64 // typical cruising speed
	SpeedVar float64 // fractional speed variation (std dev / mean)
	TurnDeg  float64 // std dev of heading change per step
	PauseP   float64 // probability, per step, of starting to slow to a stop (traffic lights, etc.)
	// MaxAccel is the most the traveller can change speed, in m/s^2. Real bodies and vehicles have
	// inertia: a car cannot go from rest to 54 km/h in a second. Without this limit the "honest"
	// traces would contain physically impossible motion (an earlier version of this simulator did,
	// and the evaluation caught it).
	MaxAccel float64
}

var (
	Walk  = Mode{"walk", 1.4, 0.25, 12, 0.04, 1.5}
	Bike  = Mode{"bike", 5.0, 0.2, 6, 0.03, 1.5}
	Drive = Mode{"drive", 14, 0.35, 4, 0.08, 3.0}
)

var origin = geo.Point{Lat: 47.6062, Lon: -122.3321}

func newRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// Honest produces n fixes at the given interval, with realistic noise: position error that varies
// with a slowly-changing accuracy, speed and heading wander, and occasional pauses.
func Honest(m Mode, n int, interval time.Duration, seed int64) *trace.Trace {
	r := newRand(seed)
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	pos := origin
	heading := r.Float64() * 360
	acc := 6 + r.Float64()*6 // metres
	dt := interval.Seconds()
	speed := m.SpeedMS
	stopFor := 0 // steps remaining at a standstill
	target := m.SpeedMS
	t := &trace.Trace{}
	for i := 0; i < n; i++ {
		// Accuracy drifts like a random walk, bounded to what consumer receivers report.
		acc = clamp(acc+r.NormFloat64()*1.2, 3, 35)
		// Pick a target speed: usually cruising with some variation, occasionally a stop of a few steps.
		if stopFor > 0 {
			stopFor--
			target = 0
		} else if r.Float64() < m.PauseP {
			stopFor = 2 + r.Intn(6)
			target = 0
		} else {
			target = math.Max(0, m.SpeedMS*(1+r.NormFloat64()*m.SpeedVar))
		}
		// Move toward the target, limited by what the body or vehicle can physically do.
		maxDelta := m.MaxAccel * dt
		speed += clamp(target-speed, -maxDelta, maxDelta)
		if speed > 0 {
			heading = math.Mod(heading+r.NormFloat64()*m.TurnDeg+360, 360)
			pos = geo.Destination(pos, heading, speed*dt)
		}
		// Measurement error: zero-mean noise with std dev about acc/2 (accuracy is ~95% radius).
		noisy := geo.Destination(pos, r.Float64()*360, math.Abs(r.NormFloat64())*acc/2)
		t.Fixes = append(t.Fixes, trace.Fix{
			Point: noisy, Time: start.Add(time.Duration(i) * interval), AccuracyM: round1(acc), Line: i + 2,
		})
	}
	return t
}

// ---- spoofing styles, from crude to careful ----

// Teleport takes an honest trace and relocates the second half to a distant city: the classic
// "set my location to Paris" jump.
func Teleport(base *trace.Trace, to geo.Point) *trace.Trace {
	out := clone(base)
	half := len(out.Fixes) / 2
	shiftLat, shiftLon := to.Lat-out.Fixes[half].Lat, to.Lon-out.Fixes[half].Lon
	for i := half; i < len(out.Fixes); i++ {
		out.Fixes[i].Lat += shiftLat
		out.Fixes[i].Lon += shiftLon
	}
	return out
}

// Static pins every fix to one fixed coordinate with a fixed accuracy, as a lazy mock-location app
// does.
func Static(n int, interval time.Duration, at geo.Point, accuracy float64) *trace.Trace {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	t := &trace.Trace{}
	for i := 0; i < n; i++ {
		t.Fixes = append(t.Fixes, trace.Fix{Point: at, Time: start.Add(time.Duration(i) * interval), AccuracyM: accuracy, Line: i + 2})
	}
	return t
}

// Linear interpolates in a perfectly straight line at constant speed between two places, with a
// fixed accuracy value: a scripted "drive" with no human or vehicle irregularity.
func Linear(a, b geo.Point, speedMS float64, interval time.Duration, accuracy float64) *trace.Trace {
	dist := geo.DistanceM(a, b)
	steps := int(math.Ceil(dist / (speedMS * interval.Seconds())))
	bearing := geo.BearingDeg(a, b)
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	t := &trace.Trace{}
	for i := 0; i <= steps; i++ {
		d := math.Min(dist, float64(i)*speedMS*interval.Seconds())
		t.Fixes = append(t.Fixes, trace.Fix{
			Point: geo.Destination(a, bearing, d), Time: start.Add(time.Duration(i) * interval), AccuracyM: accuracy, Line: i + 2,
		})
	}
	return t
}

// Careful is the hard case: an honest-looking trace of a *different* route. It takes a real
// recorded movement and replays it somewhere else, keeping all the natural noise, speed variation
// and accuracy fluctuation, so every statistical check sees plausible data. There are no
// impossible jumps. Detectors should be expected to miss this; measuring HOW often is the point.
func Careful(m Mode, n int, interval time.Duration, seed int64, to geo.Point) *trace.Trace {
	base := Honest(m, n, interval, seed)
	out := clone(base)
	shiftLat, shiftLon := to.Lat-out.Fixes[0].Lat, to.Lon-out.Fixes[0].Lon
	for i := range out.Fixes {
		out.Fixes[i].Lat += shiftLat
		out.Fixes[i].Lon += shiftLon
	}
	return out
}

// RelocatedWithClockEdits is a spoofer who also forged the timestamps so a long jump appears to have
// taken hours: defeats the speed check, but not the teleport check.
func RelocatedWithClockEdits(base *trace.Trace, to geo.Point, gap time.Duration) *trace.Trace {
	out := Teleport(base, to)
	half := len(out.Fixes) / 2
	for i := half; i < len(out.Fixes); i++ {
		out.Fixes[i].Time = out.Fixes[i].Time.Add(gap)
	}
	return out
}

func clone(t *trace.Trace) *trace.Trace {
	return &trace.Trace{Fixes: append([]trace.Fix(nil), t.Fixes...)}
}
func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func round1(v float64) float64        { return math.Round(v*10) / 10 }
