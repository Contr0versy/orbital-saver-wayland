package x11

import "github.com/jezek/xgb/xproto"

// EventKind says what happened to the window.
type EventKind int

const (
	EventResize EventKind = iota // the window changed size
	EventExpose                  // part of the window needs repainting
	EventQuit                    // time to exit; Err is set if something went wrong
)

// Event is a window event reduced to what the main loop cares about.
type Event struct {
	Kind          EventKind
	Width, Height int
	Err           error
}

// Events delivers window events. It stops producing once the surface is
// closed or the X connection is lost (after a final EventQuit).
func (s *Surface) Events() <-chan Event { return s.events }

const (
	keysymEscape = 0xff1b
	keysymQ      = 'q'
)

// pump translates raw X events into Events until the connection ends.
func (s *Surface) pump() {
	for {
		ev, err := s.conn.WaitForEvent()
		if ev == nil && err == nil {
			// Connection closed, by us or by the server.
			s.dead.Store(true)
			s.emit(Event{Kind: EventQuit})
			return
		}
		if err != nil {
			switch err.(type) {
			case xproto.WindowError, xproto.DrawableError:
				// The window we were drawing on was destroyed. For a
				// screensaver that is simply the signal to stop.
				s.emit(Event{Kind: EventQuit})
			default:
				s.emit(Event{Kind: EventQuit, Err: err})
			}
			continue
		}

		switch e := ev.(type) {
		case xproto.ConfigureNotifyEvent:
			if e.Window == s.win {
				s.emit(Event{Kind: EventResize, Width: int(e.Width), Height: int(e.Height)})
			}
		case xproto.ExposeEvent:
			if e.Count == 0 {
				s.emit(Event{Kind: EventExpose})
			}
		case xproto.DestroyNotifyEvent:
			if e.Window == s.win {
				s.emit(Event{Kind: EventQuit})
			}
		case xproto.ClientMessageEvent:
			// The window manager's close button.
			if s.owned && e.Type == s.wmProtocols && e.Format == 32 &&
				xproto.Atom(e.Data.Data32[0]) == s.wmDelete {
				s.emit(Event{Kind: EventQuit})
			}
		case xproto.KeyPressEvent:
			// Only the preview window selects key presses.
			if s.isQuitKey(e.Detail) {
				s.emit(Event{Kind: EventQuit})
			}
		}
	}
}

func (s *Surface) emit(e Event) {
	select {
	case s.events <- e:
	case <-s.done:
	}
}

// isQuitKey reports whether a key code is Escape or Q in the current layout.
func (s *Surface) isQuitKey(code xproto.Keycode) bool {
	reply, err := xproto.GetKeyboardMapping(s.conn, code, 1).Reply()
	if err != nil || len(reply.Keysyms) == 0 {
		return false
	}
	sym := reply.Keysyms[0]
	return sym == keysymEscape || sym == keysymQ
}
