// Package detect looks for signs that a GPS trace was fabricated or tampered with.
//
// Each check is independent and returns findings that say what was seen, where, and why it is
// suspicious. Checks have a Confidence: physically impossible motion is High, because no honest
// device can produce it, while statistical oddities are Low or Medium, because real devices
// occasionally produce them too. The detector reports; it does not decide guilt.
package detect

import (
	"fmt"
	"sort"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// Confidence is how strongly a finding indicates fabrication.
type Confidence int

const (
	Low Confidence = iota + 1
	Medium
	High
)

func (c Confidence) String() string {
	switch c {
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	}
	return "unknown"
}

// Finding is one suspicious observation.
type Finding struct {
	Check      string     `json:"check"`
	Confidence Confidence `json:"-"`
	Level      string     `json:"confidence"`
	Line       int        `json:"line"` // source line of the later fix involved (0 = whole trace)
	At         time.Time  `json:"time"`
	Reason     string     `json:"reason"`
}

func newFinding(check string, c Confidence, f trace.Fix, format string, a ...any) Finding {
	return Finding{Check: check, Confidence: c, Level: c.String(), Line: f.Line, At: f.Time, Reason: fmt.Sprintf(format, a...)}
}

// Check examines a time-ordered trace.
type Check interface {
	Name() string
	Run(t *trace.Trace) []Finding
}

// Config holds the thresholds. The defaults suit a person on foot, by bike or in a car; widen
// MaxSpeedMS for aircraft or trains.
type Config struct {
	// MaxSpeedMS is the fastest speed treated as physically plausible (default 85 m/s = 306 km/h).
	MaxSpeedMS float64
	// TeleportM is a single jump this far is flagged regardless of how the time gap is reported.
	TeleportM float64
	// MaxAccelMS2 is the largest plausible change of speed (default 12 m/s^2, a hard-braking car is ~9).
	MaxAccelMS2 float64
	// MinGapForSpeed ignores intervals shorter than this when computing speed, to avoid dividing
	// position noise by a tiny time.
	MinGapForSpeed time.Duration
	// MaxGap is the longest gap between fixes before the trace is treated as having a hole.
	MaxGap time.Duration
}

// DefaultConfig returns conservative thresholds.
func DefaultConfig() Config {
	return Config{
		MaxSpeedMS:     85,
		TeleportM:      50_000,
		MaxAccelMS2:    12,
		MinGapForSpeed: 500 * time.Millisecond,
		MaxGap:         10 * time.Minute,
	}
}

// All returns every check configured with cfg.
func All(cfg Config) []Check {
	return []Check{
		Ordering{},
		Speed{cfg},
		Teleport{cfg},
		Accel{cfg},
		Accuracy{},
		Smoothness{},
	}
}

// Analyze runs the checks over a trace. The trace is sorted by time first; ordering problems in the
// original are reported by the Ordering check, which sees the original order.
func Analyze(t *trace.Trace, checks []Check) []Finding {
	if t == nil || len(t.Fixes) == 0 {
		return nil
	}
	sorted, _ := t.SortedByTime()
	var out []Finding
	for _, c := range checks {
		in := sorted
		if c.Name() == "ordering" {
			in = t // needs the original order
		}
		out = append(out, c.Run(in)...)
	}
	out = dropExplained(out)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

// dropExplained removes acceleration findings that merely restate a speed or teleport finding.
// One impossible jump necessarily also looks like a huge speed change on both sides of it; reporting
// three or four lines for one event buries the useful one.
func dropExplained(fs []Finding) []Finding {
	covered := map[int]bool{}
	for _, f := range fs {
		if (f.Check == "speed" || f.Check == "teleport") && f.Line > 0 {
			covered[f.Line] = true
			covered[f.Line+1] = true // the step after a jump also "accelerates" back down
		}
	}
	out := fs[:0:0]
	for _, f := range fs {
		if f.Check == "acceleration" && covered[f.Line] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Verdict summarises findings into a single label.
type Verdict string

const (
	Clean      Verdict = "clean"
	Suspicious Verdict = "suspicious"
	Spoofed    Verdict = "likely-spoofed"
)

// Judge reduces findings to a verdict. Any High finding means likely-spoofed. Medium findings, or
// several Low ones, mean suspicious.
//
// Repetition escalates: a single lone outlier is a plausible receiver glitch, but a trace with
// repeatedly impossible readings is not one bad fix, it is a pattern, so several Medium findings
// from the movement checks also yield likely-spoofed.
func Judge(fs []Finding) Verdict {
	low, med, motionMed := 0, 0, 0
	for _, f := range fs {
		switch f.Confidence {
		case High:
			return Spoofed
		case Medium:
			med++
			if f.Check == "teleport" || f.Check == "speed" {
				motionMed++
			}
		case Low:
			low++
			if f.Check == "speed" || f.Check == "teleport" {
				motionMed++ // a repeated lone outlier is evidence too, though weaker than a Medium
			}
		}
	}
	if motionMed >= repeatedOutliers {
		return Spoofed
	}
	if med > 0 || low >= 3 {
		return Suspicious
	}
	return Clean
}

// repeatedOutliers is how many separate lone-outlier events in one trace stop being plausibly
// independent receiver glitches. Real receivers produce a wild fix rarely; five in a trace is a
// pattern.
const repeatedOutliers = 5
