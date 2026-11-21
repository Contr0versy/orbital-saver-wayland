package main

import (
	"testing"
	"time"

	"github.com/Contr0versy/orbital-background/internal/power"
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

func TestSlotAtEvenSteps(t *testing.T) {
	interval := time.Second / 60
	due := time.Unix(1000, 0)
	for _, c := range []struct {
		now  time.Duration // after due
		want time.Duration
	}{
		{-time.Millisecond, 0}, // the timer fired a hair early
		{0, 0},
		{3 * time.Millisecond, 0}, // late wake-ups land on the slot
		{interval + 5*time.Millisecond, interval},
		{5*interval + interval/2, 5 * interval}, // missed slots are skipped
	} {
		if got := slotAt(due, due.Add(c.now), interval).Sub(due); got != c.want {
			t.Errorf("slotAt(due, due%+v) = due%+v, want due%+v", c.now, got, c.want)
		}
	}
	now := time.Unix(2000, 0)
	if got := slotAt(time.Time{}, now, interval); !got.Equal(now) {
		t.Errorf("slotAt with no timetable = %v, want now", got)
	}
}

func TestNextSlotKeepsTheTimetable(t *testing.T) {
	interval := time.Second / 30
	base := time.Unix(1000, 0)
	due := nextSlot(time.Time{}, base, interval)
	for frame := 1; frame <= 300; frame++ {
		// Draw anywhere from on time to 0.9 of a slot late: the slots stay
		// put, so 300 frames take exactly 300 intervals.
		late := time.Duration(frame%10) * interval / 10
		due = nextSlot(due, due.Add(late), interval)
	}
	if want := base.Add(301 * interval); !due.Equal(want) {
		t.Errorf("slot after 300 frames = %v, want %v", due.Sub(base), want.Sub(base))
	}
	// A long stall (hidden surface) skips the missed slots instead of
	// racing to catch up.
	stalled := due.Add(10*time.Second + interval/2)
	next := nextSlot(due, stalled, interval)
	if !next.After(stalled) || next.Sub(stalled) > interval {
		t.Errorf("after a stall the next slot is %v away, want within one interval", next.Sub(stalled))
	}
	if (next.Sub(base) % interval) != 0 {
		t.Errorf("after a stall the next slot left the timetable")
	}
}
