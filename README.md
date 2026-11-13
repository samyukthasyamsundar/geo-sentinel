# geo-sentinel

A command-line tool that checks a GPS trace (CSV or GPX) for signs that it was **fabricated or
faked**, and explains each suspicion. It is a *detector*: it reports what looks wrong and how
confident it is, and it is explicit about what it cannot catch.

```
$ geosentinel testdata/fake_teleport.csv
testdata/fake_teleport.csv: 300 fixes, 2026-11-12T10:00:00Z to 2026-11-12T10:04:59Z
verdict: LIKELY-SPOOFED
2 finding(s):
  [high  ] speed     line 152  10:02:30  implied speed far beyond any vehicle (over 3,600 km/h) exceeds the plausible maximum of 306 km/h: 8040 km in 1s
  [high  ] teleport  line 152  10:02:30  position jumps 8040 km in one step, faster than any plausible vehicle
```

Exit status: `0` clean, `1` suspicious, `2` likely spoofed, `3` unreadable input, `64` bad usage,
so it can be used in scripts. `--json` prints machine-readable output.

## What it checks

| Check | Looks for | Confidence |
|-------|-----------|------------|
| `speed` | movement faster than any plausible vehicle, allowing for the reported GPS error | high |
| `teleport` | a single enormous jump, even if the timestamps were also forged to excuse it | high / medium |
| `acceleration` | speed changes no vehicle can make, after allowing for position noise | medium |
| `ordering` | timestamps that go backwards, or two positions at one instant | medium |
| `accuracy` | every fix claiming sub-metre accuracy, or one identical value throughout | low / medium |
| `smoothness` | a path with machine-regular speed and a ruler-straight heading, or frozen coordinates | low / medium |

The first four are about **physics**: no honest device can produce them. The last two are
**statistical** and weaker, because real receivers occasionally behave oddly too, so they are never
reported as high confidence. A single isolated bad fix is treated as a probable receiver glitch: it is
reported at low confidence and does **not** change the verdict by itself, and it is kept out of the
acceleration check (one bad reading corrupts the speeds on both sides of it, which used to make an honest
drive look "suspicious"). It stops being excused when it is not isolated: repeated outliers escalate, a
two-fix excursion is not a glitch, and if more than about 1% of the fixes look like isolated outliers the
trace is treated as tampered with, not glitchy (a zig-zag of displaced fixes would otherwise be absorbed).

## What it can and cannot catch

This is measured, not asserted. `go test -run Evaluation -v ./internal/detect` generates
simulated traces and counts verdicts (300 traces per row):

| Scenario | clean | suspicious | likely-spoofed |
|----------|------:|-----------:|---------------:|
| honest: walking / cycling / driving (1 s) | 100% | 0% | 0% |
| honest: driving (10 s), walking (30 s) | 100% | 0% | 0% |
| **fake:** teleport to another city | 0% | 0% | **100%** |
| **fake:** teleport *with forged timestamps* | 0% | 0% | **100%** |
| **fake:** frozen mock location | 0% | **100%** | 0% |
| **fake:** scripted straight drive | 0% | **100%** | 0% |
| **fake: careful** (real-looking motion, replayed in a different place) | **100%** | 0% | 0% |

**The last row is the important one.** A spoofer who takes genuine-looking movement, with natural
speed changes, wobble and varying accuracy, and simply replays it somewhere else produces a trace
that contains nothing impossible. **A trace-only detector cannot catch that**, and this one
doesn't (0% detected). The honest conclusion is that analysing a trace can expose crude and lazy
spoofing, but defeating a careful spoofer needs evidence from outside the trace: the device's
attestation, cell-tower and Wi-Fi corroboration, or sensor consistency. This tool is
about the first category only.

False alarms on honest traces were also checked on eight conditions that were *not* used when
choosing thresholds (slow stroll, jogging, city bus, highway, stop-and-go traffic, sparse 60 s
sampling, ...): 0 of 1,600 flagged.

**Caveat on those numbers.** The traces come from a simulator I wrote
(`internal/sim`). It models noise, speed and heading variation, stops, and a limit on
acceleration, but real GPS error is correlated over time and has heavy tails (urban canyons,
multipath), so real traces are messier. A 0% false-alarm rate on a simulator is **not** a claim
about real devices; validating against recorded real-world traces is the main open task.

## How it was tested

- **Unit tests** for geometry, both parsers, every check, the verdict logic and the CLI (exit codes,
  JSON shape, every error path).
- **Fuzzing** (Go's built-in coverage-guided fuzzer) of the CSV parser, the GPX parser and the full
  analysis. The first CSV run found a real crash within half a second (a malformed quote made the
  code ask for the position of a record that did not exist); it is fixed and kept as a regression
  test. Afterwards, in single runs of 20-60 seconds: 12.1 million CSV, 7.9 million GPX and 10.3 million
  analysis executions, no failures.
- **Mutation testing** of the parser defences: each limit (size cap, point cap, XML nesting depth,
  element length, NaN/Inf, hex floats, Unix-time range) was removed in turn to confirm a test fails.
  Two of the seven were *not* caught at first, because the tests were passing for a different
  reason; the tests were rewritten and re-checked.
- **Attacking my own fix.** After the glitch fix, I tried to hide real anomalies inside it. A zig-zag
  of 130 displaced fixes was judged *clean* because each one looked like a lone outlier; that is now
  capped (more than 1% of fixes as "outliers" means tampering), with a regression test.
- **Evaluation against a simulator** as above, which also caught real problems: the first version
  flagged 87% of honest drives (the simulator was generating impossible acceleration, and the
  acceleration check was reading GPS noise as motion), and a single 300 m GPS glitch was labelled
  "likely spoofed". Both were fixed by changing the design, not by loosening thresholds.
- A CLI bug found by testing: `--max-speed-kmh NaN` was accepted and silently disabled the speed
  check, because `NaN` compares false with everything and slipped through the range check.

## Untrusted input

Trace files are treated as hostile. Parsers are strict and report the offending **line number**;
cap input at 64 MiB and 500,000 points; reject NaN, infinity, hex floats and out-of-range
coordinates; and never panic. The GPX reader refuses nested-entity ("billion laughs") and
external-entity ("XXE") documents, caps nesting depth and element size, and accepts only UTF-8.

## Build and run

```sh
go build -o geosentinel ./cmd/geosentinel
./geosentinel path/to/trace.csv
./geosentinel --json --max-speed-kmh 900 flight.gpx   # raise the limit for aircraft
go test ./...
```

CSV needs a header with `time`, `lat`, `lon` (and optionally `accuracy`, in metres); `time` is
RFC 3339 or Unix seconds. GPX `<trkpt>` elements are read; waypoints and routes are ignored.
`go run ./cmd/gensamples` regenerates the examples in `testdata/` (honest and faked traces).

## Layout

| Path | Purpose |
|------|---------|
| `internal/geo` | great-circle distance, bearing, destination point |
| `internal/trace` | CSV and GPX parsing, validation, limits |
| `internal/detect` | the checks, verdict logic, and the evaluation tests |
| `internal/sim` | honest and spoofed trace generators, used by the tests |
| `cmd/geosentinel` | the command-line tool |
| `cmd/gensamples` | writes the example traces |

## Limitations

- Detects only what is visible **in the trace**; see the careful-spoofer result above.
- Thresholds are for people and ordinary vehicles. Use `--max-speed-kmh` for trains and aircraft;
  a flight with the default limit will be called spoofed.
- Statistical checks need at least 20 fixes and stay silent below that.
- Validated against a simulator only, not against recorded real traces.
- The repeated-outlier threshold (5), the glitch ceiling (200 km) and the 1% outlier cap are judgement
  calls, not derived from data. In the simulator honest traces contain no isolated outliers at all
  (0 in 1,500), but real receivers produce heavy-tailed glitches, so the cap may need tuning on real data.
- Evaluates one trace at a time; it cannot compare against other people's or devices' traces.

This is a defensive analysis tool. It does not generate or modify location data for use against a
service (the trace generators exist only to test the detector).

## License

MIT, see LICENSE.
