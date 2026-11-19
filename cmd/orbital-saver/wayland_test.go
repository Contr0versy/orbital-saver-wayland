package main

import (
	"testing"
	"time"

	"github.com/codetheuri/orbital-saver/internal/power"
)

func TestSnapFPS(t *testing.T) {
	for _, c := range []struct{ fps, refresh, want int }{
		{30, 120000, 30},
		{25, 120000, 24},
		{20, 120000, 20},
		{13, 120000, 12},
		{20, 59950, 20},
		{25, 60000, 20},
		{144, 120000, 120},
		{7, 0, 7}, // refresh unknown
		{0, 120000, 0},
	} {
		if got := snapFPS(c.fps, c.refresh); got != c.want {
			t.Errorf("snapFPS(%d, %d) = %d, want %d", c.fps, c.refresh, got, c.want)
		}
	}
}

func TestTargetFPS(t *testing.T) {
	o := waylandOptions{fps: 30, batteryFPS: 20, freezeBelow: 20}
	for _, c := range []struct {
		st     power.State
		paused bool
		want   int
	}{
		{power.State{OnAC: true, Capacity: 5}, false, 30},
		{power.State{OnAC: true}, true, 0},
		{power.State{Capacity: 50}, false, 20},
		{power.State{Capacity: 19}, false, 0},
		{power.State{Capacity: -1}, false, 20},
	} {
		if got := targetFPS(o, c.st, c.paused); got != c.want {
			t.Errorf("targetFPS(%+v, paused=%v) = %d, want %d", c.st, c.paused, got, c.want)
		}
	}
}

func TestNextFrameLocksToRefresh(t *testing.T) {
	const period = time.Second / 120
	interval := time.Second / 60
	base := time.Unix(1000, 0)
	// The last frame was drawn 1 ms after a refresh; its callback came one
	// cycle later. The next frame lands exactly two cycles after the first
	// refresh, whatever the timer jitter in lastDraw.
	for _, jitter := range []time.Duration{0, time.Millisecond, 3 * time.Millisecond} {
		lastDraw := base.Add(jitter)
		vblank := base.Add(period)
		if got, want := nextFrame(lastDraw, vblank, interval, period), base.Add(2*period); !got.Equal(want) {
			t.Errorf("jitter %v: next = %v, want %v", jitter, got.Sub(base), want.Sub(base))
		}
	}
	// Refresh rate unknown: plain interval.
	if got := nextFrame(base, time.Time{}, interval, 0); !got.Equal(base.Add(interval)) {
		t.Errorf("unlocked next = %v", got.Sub(base))
	}
}
