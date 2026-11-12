package geo

import (
	"math"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v +/- %v", name, got, want, tol)
	}
}

func TestDistanceKnownPairs(t *testing.T) {
	// Reference values from the haversine formula on a 6371.0088 km sphere.
	london := Point{51.5074, -0.1278}
	paris := Point{48.8566, 2.3522}
	near(t, "London-Paris", DistanceM(london, paris), 343_500, 1500)

	nyc := Point{40.7128, -74.0060}
	la := Point{34.0522, -118.2437}
	near(t, "NYC-LA", DistanceM(nyc, la), 3_936_000, 15_000)

	// One degree of latitude is about 111.2 km everywhere.
	near(t, "1 deg lat", DistanceM(Point{0, 0}, Point{1, 0}), 111_195, 150)
}

func TestDistanceProperties(t *testing.T) {
	a, b := Point{10, 20}, Point{-33, 151}
	if DistanceM(a, a) != 0 {
		t.Error("distance to self must be 0")
	}
	if d1, d2 := DistanceM(a, b), DistanceM(b, a); math.Abs(d1-d2) > 1e-6 {
		t.Errorf("not symmetric: %v vs %v", d1, d2)
	}
	// Antipodal points are half the circumference and must not produce NaN.
	d := DistanceM(Point{0, 0}, Point{0, 180})
	if math.IsNaN(d) {
		t.Fatal("NaN for antipodal points")
	}
	near(t, "antipodal", d, math.Pi*earthRadiusM, 1)
	// Across the antimeridian is short, not ~40,000 km.
	if d := DistanceM(Point{0, 179.9999}, Point{0, -179.9999}); d > 30 {
		t.Errorf("antimeridian crossing = %v m, want a few metres", d)
	}
}

func TestBearing(t *testing.T) {
	o := Point{0, 0}
	near(t, "north", BearingDeg(o, Point{1, 0}), 0, 0.01)
	near(t, "east", BearingDeg(o, Point{0, 1}), 90, 0.01)
	near(t, "south", BearingDeg(o, Point{-1, 0}), 180, 0.01)
	near(t, "west", BearingDeg(o, Point{0, -1}), 270, 0.01)
}

func TestAngleDiff(t *testing.T) {
	cases := []struct{ a, b, want float64 }{{10, 350, 20}, {0, 180, 180}, {90, 90, 0}, {359, 1, 2}, {270, 90, 180}}
	for _, c := range cases {
		near(t, "diff", AngleDiffDeg(c.a, c.b), c.want, 1e-9)
	}
}

func TestDestinationRoundTrip(t *testing.T) {
	start := Point{47.6, -122.3}
	for _, br := range []float64{0, 45, 90, 135, 180, 225, 270, 315} {
		for _, d := range []float64{1, 100, 5000, 250_000} {
			end := Destination(start, br, d)
			near(t, "distance", DistanceM(start, end), d, d*1e-6+1e-6)
			if d >= 100 {
				near(t, "bearing", AngleDiffDeg(BearingDeg(start, end), br), 0, 0.01)
			}
		}
	}
}

func TestValid(t *testing.T) {
	bad := []Point{{91, 0}, {-91, 0}, {0, 181}, {0, -181}, {math.NaN(), 0}, {0, math.Inf(1)}}
	for _, p := range bad {
		if p.Valid() {
			t.Errorf("%v should be invalid", p)
		}
	}
	for _, p := range []Point{{0, 0}, {90, 180}, {-90, -180}} {
		if !p.Valid() {
			t.Errorf("%v should be valid", p)
		}
	}
}
