package main

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codetheuri/orbital-saver/internal/niri"
	"github.com/codetheuri/orbital-saver/internal/power"
	"github.com/codetheuri/orbital-saver/internal/scene"
	"github.com/codetheuri/orbital-saver/internal/wayland"
)

type waylandOptions struct {
	screensaver  bool
	output       string
	scale        int
	fps          int // on AC
	batteryFPS   int // 0 freezes
	freezeBelow  int // percent; 0 never freezes
	coveredBelow int // freeze while less than this percent shows between windows; 0 never
	fullRedraw   bool
	damageRects  int
	dmabuf       bool
	stats        bool // print frame counts on exit
	speed        float64
	lineWidth    int
	palette      scene.Palette
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
		DMABuf:     o.dmabuf,
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

	// Under niri, freeze while windows cover (nearly) all of the wallpaper.
	// A screensaver covers the windows instead, so this is for wallpapers.
	var layouts <-chan niri.State
	var layout *niri.State
	if !o.screensaver && o.coveredBelow > 0 {
		layouts, _ = niri.Watch() // not niri, or no socket: always animate
	}

	timer := time.NewTimer(time.Hour)
	stopTimer(timer)

	var (
		paused  bool
		waiting bool // a commit is out and its frame callback has not come
		redraw  = true
		fps     int
		due     time.Time // the next frame slot; slots are 1/fps apart
		last    = time.Now()
		start   = last
		drawn   int // frames rendered
		commits int // frames sent to the compositor
		frames  int // frame callbacks received
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
			fmt.Fprintf(os.Stderr, "orbital-saver: total %.1f s, %d frames drawn, %d committed (%.1f/s), %d frame callbacks; %s\n",
				secs, drawn, commits, float64(commits)/secs, frames, surface.Buffers())
		}()
	}
	updateRate := func() {
		covered := false
		if layout != nil {
			name, w, h := surface.Output()
			covered = layout.Visible(name, float64(w), float64(h)) < float64(o.coveredBelow)/100
		}
		rate := snapFPS(targetFPS(o, supply, paused || covered), surface.Refresh())
		if rate != fps {
			fps = rate
			due = time.Time{} // start a new timetable at the next frame
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
		case due.IsZero():
			timer.Reset(0)
		default:
			timer.Reset(time.Until(due))
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
		// A redraw for a configure between slots leaves the timetable alone.
		if fps > 0 && (due.IsZero() || !now.Before(due.Add(-time.Millisecond))) {
			due = nextSlot(due, now, time.Second/time.Duration(fps))
		}
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
				frames++
			}
			if p.Configure {
				redraw = true
				updateRate() // the output, and so its refresh rate, may have changed
			}

		case supply = <-supplyChanges:
			updateRate()

		case st, ok := <-layouts:
			if ok {
				layout = &st
			} else {
				layouts, layout = nil, nil // niri went away
			}
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

// nextSlot returns the frame slot after due on a fixed timetable of one
// slot per interval, skipping slots that have already passed. Keeping to a
// timetable, rather than timing each frame from the last one, means a late
// frame does not push every later frame back, nor an early one pull them
// forward: the average rate is exactly the target.
func nextSlot(due, now time.Time, interval time.Duration) time.Time {
	if due.IsZero() {
		return now.Add(interval)
	}
	due = due.Add(interval)
	if !due.After(now) {
		due = due.Add((now.Sub(due)/interval + 1) * interval)
	}
	return due
}

// targetFPS applies the power policy: full rate on AC, a lower one on
// battery, and none at all (a still picture) when paused, covered by
// windows or nearly empty.
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
