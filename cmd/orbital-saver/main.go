// orbital-saver is a lightweight X11 screensaver: a retro wireframe solar
// system with Keplerian orbits, rendered on the CPU.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/codetheuri/orbital-saver/internal/scene"
	"github.com/codetheuri/orbital-saver/internal/x11"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usageText = `orbital-saver %s - retro wireframe solar system screensaver for X11

Usage:
  orbital-saver [--preview | --root | --window-id ID] [options]

Target (default: --preview, or --root when $XSCREENSAVER_WINDOW is set):
  --preview          open a resizable desktop window (Esc or q closes it)
  --root             draw on the root window, or on the window a screensaver
                     daemon passes in $XSCREENSAVER_WINDOW
  --window-id ID     draw on an existing window; ID is hex (0x...) or decimal

Options:
  --palette NAME     colour theme: %s (default amber)
  --fps N            target frame rate, 1-240 (default 60)
  --speed X          time multiplier; 2 is twice as fast, 0 freezes (default 1)
  --stars N          number of background stars (default 1500)
  --line-width N     line thickness in pixels; 0 = automatic (default 0)
  --seed N           random seed for the layout; 0 = different every run (default 0)
  --version          print the version and exit

Single-dash forms (-root, -window-id ...) are accepted as well.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orbital-saver:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		preview     = flag.Bool("preview", false, "")
		root        = flag.Bool("root", false, "")
		windowID    = flag.String("window-id", "", "")
		paletteName = flag.String("palette", "amber", "")
		fps         = flag.Int("fps", 60, "")
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
		fmt.Println("orbital-saver", version)
		return nil
	}
	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see --help)", flag.Arg(0))
	}
	if *fps < 1 || *fps > 240 {
		return fmt.Errorf("--fps must be between 1 and 240, got %d", *fps)
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

	cfg := x11.Config{
		Title:      "orbital-saver",
		Width:      960,
		Height:     600,
		Background: palette.Background,
		Ramp:       palette.Ramp(),
		LineWidth:  *lineWidth,
	}
	// An explicit window wins over --root, which wins over --preview, so a
	// daemon that passes several of them still gets what it expects.
	switch {
	case *windowID != "":
		id, err := x11.ParseWindowID(*windowID)
		if err != nil {
			return err
		}
		cfg.Mode, cfg.Window = x11.ModeWindow, id
	case *root:
		cfg.Mode = x11.ModeRoot
	case *preview:
		cfg.Mode = x11.ModePreview
	case os.Getenv("XSCREENSAVER_WINDOW") != "":
		cfg.Mode = x11.ModeRoot
	default:
		cfg.Mode = x11.ModePreview
	}

	// Everything here is I/O plumbing around one drawing loop; nothing runs
	// in parallel. xgb passes each request between goroutines, and with a
	// single scheduler thread those hand-offs stay cheap instead of waking
	// other OS threads thousands of times a second.
	runtime.GOMAXPROCS(1)

	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}
	system := scene.New(*seed, *stars)

	surface, err := x11.Open(cfg)
	if err != nil {
		return err
	}
	// Frees the pixmap and graphics context on every exit path below.
	defer surface.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	ticker := time.NewTicker(time.Second / time.Duration(*fps))
	defer ticker.Stop()

	width, height := surface.Size()
	last := time.Now()
	for {
		select {
		case <-signals:
			return nil

		case ev := <-surface.Events():
			switch ev.Kind {
			case x11.EventQuit:
				return ev.Err
			case x11.EventResize:
				// Only remember the size here. A drag-resize sends a burst
				// of these, and the back buffer is rebuilt once per frame.
				width, height = ev.Width, ev.Height
			case x11.EventExpose:
				surface.Blit()
			}

		case <-ticker.C:
			if err := surface.Resize(width, height); err != nil {
				return err
			}
			now := time.Now()
			// Cap the step so a stall (suspend, a stopped process) does
			// not make the planets jump.
			dt := math.Min(now.Sub(last).Seconds(), 0.1)
			last = now

			system.Advance(dt * *speed)
			system.Draw(surface, width, height)
			if err := surface.Present(); err != nil {
				return err
			}
		}
	}
}
