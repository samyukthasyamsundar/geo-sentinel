// gensamples writes the example traces in testdata/ from the simulator, deterministically.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/sim"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

func main() {
	dir := "testdata"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	paris := geo.Point{Lat: 48.8566, Lon: 2.3522}
	files := map[string]*trace.Trace{
		"honest_drive.csv":  sim.Honest(sim.Drive, 300, time.Second, 11),
		"honest_walk.csv":   sim.Honest(sim.Walk, 300, time.Second, 12),
		"fake_teleport.csv": sim.Teleport(sim.Honest(sim.Drive, 300, time.Second, 11), paris),
		"fake_frozen.csv":   sim.Static(300, time.Second, geo.Point{Lat: 47.6062, Lon: -122.3321}, 5),
		"fake_scripted.csv": sim.Linear(geo.Point{Lat: 47.6, Lon: -122.3}, geo.Point{Lat: 47.65, Lon: -122.2}, 14, time.Second, 5),
		"fake_careful.csv":  sim.Careful(sim.Drive, 300, time.Second, 11, paris),
	}
	for name, tr := range files {
		if err := writeCSV(filepath.Join(dir, name), tr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d files to %s\n", len(files), dir)
}

func writeCSV(path string, tr *trace.Trace) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, "time,lat,lon,accuracy")
	for _, x := range tr.Fixes {
		fmt.Fprintf(f, "%s,%.6f,%.6f,%.1f\n", x.Time.Format(time.RFC3339), x.Lat, x.Lon, x.AccuracyM)
	}
	return nil
}
