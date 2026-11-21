// orbital-background is a lightweight live wallpaper: a retro wireframe
// solar system with Keplerian orbits, rendered on the CPU. It runs natively
// on Wayland compositors with wlr-layer-shell.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Contr0versy/orbital-background/internal/scene"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usageText = `orbital-background %s - retro wireframe solar system live wallpaper

Usage:
  orbital-background [options]

Draws on the background layer, under every window. Needs a Wayland
compositor with wlr-layer-shell, e.g. niri or sway.

  --output NAME      output to cover, e.g. eDP-1 (default: compositor's choice);
                     run one process per output for several screens
  --scale N          buffer scale, 1-4; 0 follows the output (default 0).
                     1 on a HiDPI screen draws a quarter of the pixels
  --battery-fps N    frame rate on battery; 0 freezes the picture (default 60)
  --freeze-below P   on battery, freeze below P percent charge; 0 never (default 20)
  --skip-covered     on niri, animate only what shows between windows, found
                     by comparing a screen capture with what was drawn after
                     each layout change; freeze when nothing shows (default
                     true; --skip-covered=false animates everything)
  --covered-fps N    with --skip-covered, frame rate while at least
                     --covered-at percent of the screen is hidden; the full
                     rate is kept while about half or less is hidden. 0 uses
                     the normal rate throughout (default 20)
  --covered-at P     percent of the screen hidden before --covered-fps
                     applies, 1-100 (default 60)
  --full-redraw      redraw the whole frame every time (for power comparisons)
  --damage-rects N   most damage rects per frame before they are merged (default 256)
  --dmabuf=false     send frames in shared memory instead of GPU buffers
                     (udmabuf). GPU buffers save the compositor a copy per
                     frame; without access to /dev/udmabuf, or with a
                     compositor that cannot take them, shared memory is used
  --stats            print frame and commit counts, and the buffer type
  Signals: SIGUSR1 freezes the picture, SIGUSR2 resumes.

Options:
  --palette NAME     colour theme: %s (default amber)
  --fps N            frame rate on AC, 1-240 (default 120), rounded to divide
                     the refresh rate
  --speed X          time multiplier; 2 is twice as fast, 0 freezes (default 1)
  --stars N          number of background stars (default 1500)
  --line-width N     line thickness in logical pixels; 0 = automatic (default 0)
  --seed N           random seed for the layout; 0 = different every run (default 0)
  --version          print the version and exit

Single-dash forms (-output, -palette ...) are accepted as well.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orbital-background:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		outputName  = flag.String("output", "", "")
		scale       = flag.Int("scale", 0, "")
		batteryFPS  = flag.Int("battery-fps", 60, "")
		freezeBelow = flag.Int("freeze-below", 20, "")
		skipCovered = flag.Bool("skip-covered", true, "")
		coveredFPS  = flag.Int("covered-fps", 20, "")
		coveredAt   = flag.Int("covered-at", 60, "")
		fullRedraw  = flag.Bool("full-redraw", false, "")
		damageRects = flag.Int("damage-rects", 256, "")
		dmabuf      = flag.Bool("dmabuf", true, "")
		stats       = flag.Bool("stats", false, "")
		paletteName = flag.String("palette", "amber", "")
		fps         = flag.Int("fps", 120, "")
		speed       = flag.Float64("speed", 1, "")
		stars       = flag.Int("stars", 1500, "")
		lineWidth   = flag.Int("line-width", 0, "")
		seed        = flag.Int64("seed", 0, "")
		showVersion = flag.Bool("version", false, "")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), usageText,
			version, strings.Join(scene.PaletteNames(), ", "))
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("orbital-background", version)
		return nil
	}
	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see --help)", flag.Arg(0))
	}
	if *fps < 1 || *fps > 240 {
		return fmt.Errorf("--fps must be between 1 and 240, got %d", *fps)
	}
	if *batteryFPS < 0 || *batteryFPS > 240 {
		return fmt.Errorf("--battery-fps must be between 0 and 240, got %d", *batteryFPS)
	}
	if *freezeBelow < 0 || *freezeBelow > 100 {
		return fmt.Errorf("--freeze-below must be between 0 and 100, got %d", *freezeBelow)
	}
	if *coveredFPS < 0 || *coveredFPS > 240 {
		return fmt.Errorf("--covered-fps must be between 0 and 240, got %d", *coveredFPS)
	}
	if *coveredAt < 1 || *coveredAt > 100 {
		return fmt.Errorf("--covered-at must be between 1 and 100, got %d", *coveredAt)
	}
	if *scale < 0 || *scale > 4 {
		return fmt.Errorf("--scale must be between 0 and 4, got %d", *scale)
	}
	if *damageRects < 1 {
		return fmt.Errorf("--damage-rects must be at least 1, got %d", *damageRects)
	}
	if *stars < 0 || *stars > 100000 {
		return fmt.Errorf("--stars must be between 0 and 100000, got %d", *stars)
	}
	if *lineWidth < 0 || *lineWidth > 16 {
		return fmt.Errorf("--line-width must be between 0 and 16, got %d", *lineWidth)
	}
	if math.IsNaN(*speed) || math.IsInf(*speed, 0) {
		return fmt.Errorf("--speed must be a finite number")
	}
	palette, err := scene.PaletteByName(*paletteName)
	if err != nil {
		return err
	}
	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}

	// Everything here is I/O plumbing around one drawing loop; nothing
	// runs in parallel, and a single scheduler thread keeps the hand-offs
	// between the event and drawing goroutines cheap.
	runtime.GOMAXPROCS(1)
	return runWayland(waylandOptions{
		output:      *outputName,
		scale:       *scale,
		fps:         *fps,
		batteryFPS:  *batteryFPS,
		freezeBelow: *freezeBelow,
		skipCovered: *skipCovered,
		coveredFPS:  *coveredFPS,
		coveredAt:   *coveredAt,
		fullRedraw:  *fullRedraw,
		damageRects: *damageRects,
		dmabuf:      *dmabuf,
		stats:       *stats,
		speed:       *speed,
		lineWidth:   *lineWidth,
		palette:     palette,
	}, scene.New(*seed, *stars))
}
