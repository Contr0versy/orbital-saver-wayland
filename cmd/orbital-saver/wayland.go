package main

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codetheuri/orbital-saver/internal/power"
	"github.com/codetheuri/orbital-saver/internal/scene"
	"github.com/codetheuri/orbital-saver/internal/wayland"
)

type waylandOptions struct {
	screensaver bool
	output      string
	scale       int
	fps         int // on AC
	batteryFPS  int // 0 freezes
	freezeBelow int // percent; 0 never freezes
	fullRedraw  bool
	damageRects int
	stats       bool // print frame counts on exit
	speed       float64
	lineWidth   int
	palette     scene.Palette
}

// runWayland is the frame loop for a layer-shell surface. Unlike the X11
// loop it is driven by the compositor: after each commit it waits for the
// frame callback before drawing again. While the surface is hidden (under
// a lock screen, on a screen that is off) the callback never comes, and the
// loop sleeps with no timers running. A frame with no visible change is
// not committed at all.
func runWayland(o waylandOptions, system *scene.Scene) error {
	cfg := wayland.Config{
		Layer:      wayland.LayerBackground,
		Namespace:  "orbital-saver",
		Output:     o.output,
		Scale:      o.scale,
		Background: o.palette.Background,
		Ramp:       o.palette.Ramp(),
		LineWidth:  o.lineWidth,
		FullRedraw: o.fullRedraw,
		MaxRects:   o.damageRects,
	}
	if o.screensaver {
		cfg.Layer = wayland.LayerOverlay
		cfg.Namespace = "orbital-saver-screensaver"
	}
	surface, err := wayland.Open(cfg)
	if err != nil {
		return err
	}
	defer surface.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2)

	supply := power.Read(power.SysfsRoot)
	supplyChanges, err := power.Watch(power.SysfsRoot)
	if err != nil {
		// Keep running at the AC rate rather than fail over a missing
		// convenience.
		fmt.Fprintln(os.Stderr, "orbital-saver: cannot watch the power supply:", err)
	}

	timer := time.NewTimer(time.Hour)
	stopTimer(timer)

	var (
		paused   bool
		waiting  bool // a commit is out and its frame callback has not come
		redraw   = true
		fps      int
		period   time.Duration // one refresh cycle of the output, 0 if unknown
		vblank   time.Time     // when the last frame callback arrived
		last     = time.Now()
		lastDraw time.Time
		start    = last
		drawn    int // frames rendered
		commits  int // frames sent to the compositor
		frames   int // frame callbacks received
	)
	// With --stats, a line every statsEvery and one at exit. The periodic
	// lines show whether the compositor stops asking for frames, e.g. under
	// a fullscreen window or the lock screen.
	var statsTick <-chan time.Time
	var tickCommits, tickFrames int
	if o.stats {
		const statsEvery = 5 * time.Second
		ticker := time.NewTicker(statsEvery)
		defer ticker.Stop()
		statsTick = ticker.C
		defer func() {
			secs := time.Since(start).Seconds()
			fmt.Fprintf(os.Stderr, "orbital-saver: total %.1f s, %d frames drawn, %d committed (%.1f/s), %d frame callbacks\n",
				secs, drawn, commits, float64(commits)/secs, frames)
		}()
	}
	updateRate := func() {
		refresh := surface.Refresh()
		fps = snapFPS(targetFPS(o, supply, paused), refresh)
		period = 0
		if refresh > 0 {
			period = time.Duration(1e12 / int64(refresh)) // refresh is in mHz
		}
	}
	updateRate()

	// schedule arms the timer for the next frame, or leaves it off when
	// the loop should sleep until something happens.
	schedule := func() {
		stopTimer(timer)
		switch {
		case redraw:
			timer.Reset(0)
		case waiting || fps == 0:
			// The frame callback, or a change of policy, will call us.
		default:
			timer.Reset(time.Until(nextFrame(lastDraw, vblank, time.Second/time.Duration(fps), period)))
		}
	}

	draw := func() error {
		w, h, ok := surface.Begin()
		if !ok {
			// Not configured yet. The configure event sets redraw again.
			redraw = false
			return nil
		}
		now := time.Now()
		// Cap the step so a stall (suspend, a hidden surface, a freeze)
		// does not make the planets jump.
		dt := math.Min(now.Sub(last).Seconds(), 0.1)
		last = now
		if fps > 0 {
			system.Advance(dt * o.speed)
		}
		system.Draw(surface, w, h)
		committed, err := surface.Present()
		if err != nil {
			return surface.Explain(err)
		}
		redraw = false
		drawn++
		if committed {
			commits++
		}
		waiting = waiting || committed
		lastDraw = now
		return nil
	}

	for {
		select {
		case sig := <-signals:
			switch sig {
			case syscall.SIGUSR1:
				paused = true
			case syscall.SIGUSR2:
				paused = false
			default:
				return nil
			}
			updateRate()

		case <-surface.Notify():
			p := surface.Pending()
			if p.Quit {
				return p.Err
			}
			if p.Frame {
				waiting = false
				vblank = time.Now()
				frames++
			}
			if p.Configure {
				redraw = true
				updateRate() // the output, and so its refresh rate, may have changed
			}

		case supply = <-supplyChanges:
			updateRate()

		case <-timer.C:
			if err := draw(); err != nil {
				return err
			}

		case <-statsTick:
			fmt.Fprintf(os.Stderr, "orbital-saver: %s  %2d fps target, %5.1f commits/s, %5.1f frame callbacks/s\n",
				time.Now().Format("15:04:05"), fps,
				float64(commits-tickCommits)/5, float64(frames-tickFrames)/5)
			tickCommits, tickFrames = commits, frames
			continue // nothing to reschedule
		}
		schedule()
	}
}

// nextFrame picks when to draw the next frame: one interval after the last
// draw, moved to the nearest whole number of refresh cycles after the last
// frame callback. Locking to the display's own rhythm keeps every frame on
// screen for the same number of cycles; a free-running timer drifts against
// it and now and then shows a frame for one cycle too many, which reads as
// a stutter.
func nextFrame(lastDraw, vblank time.Time, interval, period time.Duration) time.Time {
	next := lastDraw.Add(interval)
	if period <= 0 || vblank.IsZero() {
		return next
	}
	n := (next.Sub(vblank) + period/2) / period
	if n < 0 {
		n = 0
	}
	return vblank.Add(n * period)
}

// targetFPS applies the power policy: full rate on AC, a lower one on
// battery, and none at all (a still picture) when paused or nearly empty.
func targetFPS(o waylandOptions, st power.State, paused bool) int {
	switch {
	case paused:
		return 0
	case st.OnAC:
		return o.fps
	case o.freezeBelow > 0 && st.Capacity >= 0 && st.Capacity < o.freezeBelow:
		return 0
	}
	return o.batteryFPS
}

// snapFPS lowers fps to the nearest rate that divides the refresh rate
// (given in mHz), so every frame is shown for the same number of refresh
// cycles. On a 120 Hz panel 25 becomes 24; 30 and 20 stay.
func snapFPS(fps, refreshMHz int) int {
	hz := (refreshMHz + 500) / 1000
	if fps <= 0 || hz <= 0 {
		return fps
	}
	if fps >= hz {
		return hz
	}
	for f := fps; f > 1; f-- {
		if hz%f == 0 {
			return f
		}
	}
	return 1
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
