// Package geo holds the small amount of geometry the detectors need.
package geo

import "math"

const earthRadiusM = 6371008.8 // mean Earth radius in metres (IUGG)

// Point is a WGS-84 position.
type Point struct {
	Lat, Lon float64 // degrees
}

// Valid reports whether the coordinates are finite and in range.
func (p Point) Valid() bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lon) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lon, 0) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// DistanceM is the great-circle (haversine) distance between two points, in metres.
// Haversine is accurate to about 0.5% on a sphere, which is far inside the tolerances
// the detectors use (they compare against speeds that differ by factors of ten).
func DistanceM(a, b Point) float64 {
	dLat := rad(b.Lat - a.Lat)
	dLon := rad(b.Lon - a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	if h > 1 { // guard against rounding just above 1 for antipodal points
		h = 1
	}
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// BearingDeg is the initial bearing from a to b in degrees [0, 360).
func BearingDeg(a, b Point) float64 {
	la1, la2 := rad(a.Lat), rad(b.Lat)
	dLon := rad(b.Lon - a.Lon)
	y := math.Sin(dLon) * math.Cos(la2)
	x := math.Cos(la1)*math.Sin(la2) - math.Sin(la1)*math.Cos(la2)*math.Cos(dLon)
	d := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(d+360, 360)
}

// AngleDiffDeg is the smallest absolute difference between two bearings, in [0, 180].
func AngleDiffDeg(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// Destination returns the point reached by travelling distM metres from p on the given bearing.
func Destination(p Point, bearingDeg, distM float64) Point {
	ang := distM / earthRadiusM
	br := rad(bearingDeg)
	la1, lo1 := rad(p.Lat), rad(p.Lon)
	la2 := math.Asin(math.Sin(la1)*math.Cos(ang) + math.Cos(la1)*math.Sin(ang)*math.Cos(br))
	lo2 := lo1 + math.Atan2(math.Sin(br)*math.Sin(ang)*math.Cos(la1), math.Cos(ang)-math.Sin(la1)*math.Sin(la2))
	lon := math.Mod(lo2*180/math.Pi+540, 360) - 180
	return Point{Lat: la2 * 180 / math.Pi, Lon: lon}
}
