package x11

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// ParseWindowID parses an X11 window handle given as hex ("0x3a00007") or
// decimal ("60817415"), the two forms screensaver daemons pass on the
// command line and in $XSCREENSAVER_WINDOW.
func ParseWindowID(s string) (uint32, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(s), 0, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid window id %q: expected hex (0x...) or decimal", s)
	}
	if id == 0 {
		return 0, fmt.Errorf("invalid window id %q: must not be zero", s)
	}
	return uint32(id), nil
}

// resolveRoot finds the window that "--root" should draw on. In order:
//
//  1. $XSCREENSAVER_WINDOW, which xscreensaver, xfce4-screensaver,
//     mate-screensaver and friends export to the hacks they launch;
//  2. a virtual root advertised through the __SWM_VROOT property;
//  3. the real root window of the screen.
func resolveRoot(conn *xgb.Conn, root xproto.Window) xproto.Window {
	if env := os.Getenv("XSCREENSAVER_WINDOW"); env != "" {
		if id, err := ParseWindowID(env); err == nil {
			return xproto.Window(id)
		}
	}
	if vroot := virtualRoot(conn, root); vroot != 0 {
		return vroot
	}
	return root
}

// virtualRoot looks for a child of the root window carrying __SWM_VROOT.
func virtualRoot(conn *xgb.Conn, root xproto.Window) xproto.Window {
	atom, err := xproto.InternAtom(conn, true, uint16(len("__SWM_VROOT")), "__SWM_VROOT").Reply()
	if err != nil || atom.Atom == 0 {
		return 0 // nobody has ever set the property
	}
	tree, err := xproto.QueryTree(conn, root).Reply()
	if err != nil {
		return 0
	}
	// Send every request before reading any reply: one round trip in total.
	cookies := make([]xproto.GetPropertyCookie, len(tree.Children))
	for i, child := range tree.Children {
		cookies[i] = xproto.GetProperty(conn, false, child, atom.Atom, xproto.AtomWindow, 0, 1)
	}
	var found xproto.Window
	for _, cookie := range cookies {
		reply, err := cookie.Reply()
		if err == nil && found == 0 && reply.Format == 32 && len(reply.Value) >= 4 {
			found = xproto.Window(xgb.Get32(reply.Value))
		}
	}
	return found
}

// createWindow opens the standalone preview window.
func (s *Surface) createWindow(screen *xproto.ScreenInfo, title string) error {
	wid, err := xproto.NewWindowId(s.conn)
	if err != nil {
		return err
	}
	err = xproto.CreateWindowChecked(s.conn, screen.RootDepth, wid, screen.Root,
		0, 0, s.width, s.height, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		[]uint32{
			s.bg,
			xproto.EventMaskStructureNotify | xproto.EventMaskExposure | xproto.EventMaskKeyPress,
		}).Check()
	if err != nil {
		return fmt.Errorf("cannot create preview window: %w", err)
	}
	s.win, s.owned = wid, true

	setText := func(prop, typ xproto.Atom, value string) {
		xproto.ChangeProperty(s.conn, xproto.PropModeReplace, wid, prop, typ,
			8, uint32(len(value)), []byte(value))
	}
	setText(xproto.AtomWmName, xproto.AtomString, title)
	setText(xproto.AtomWmClass, xproto.AtomString, "orbital-saver\x00Orbital-saver\x00")
	if utf8 := s.atom("UTF8_STRING"); utf8 != 0 {
		setText(s.atom("_NET_WM_NAME"), utf8, title)
	}

	// Ask the window manager to tell us about the close button instead of
	// killing the connection.
	s.wmProtocols = s.atom("WM_PROTOCOLS")
	s.wmDelete = s.atom("WM_DELETE_WINDOW")
	data := make([]byte, 4)
	xgb.Put32(data, uint32(s.wmDelete))
	xproto.ChangeProperty(s.conn, xproto.PropModeReplace, wid, s.wmProtocols,
		xproto.AtomAtom, 32, 1, data)

	xproto.MapWindow(s.conn, wid)
	return nil
}

// adoptWindow prepares a window owned by someone else (the root window or a
// screensaver daemon's window) for drawing.
func (s *Surface) adoptWindow(setup *xproto.SetupInfo, win xproto.Window) error {
	geom, err := xproto.GetGeometry(s.conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return fmt.Errorf("window 0x%x does not exist on this display", uint32(win))
	}
	attrs, err := xproto.GetWindowAttributes(s.conn, win).Reply()
	if err != nil {
		return fmt.Errorf("0x%x is not a window", uint32(win))
	}
	if attrs.Class == xproto.WindowClassInputOnly {
		return fmt.Errorf("window 0x%x is input-only and cannot be drawn on", uint32(win))
	}

	screen := setup.DefaultScreen(s.conn)
	for i := range setup.Roots {
		if setup.Roots[i].Root == geom.Root {
			screen = &setup.Roots[i]
		}
	}
	s.win = win
	s.depth = geom.Depth
	s.width, s.height = geom.Width, geom.Height
	s.format = lookupFormat(screen, attrs.Visual)
	s.colormap = attrs.Colormap
	if s.colormap == 0 {
		s.colormap = screen.DefaultColormap
	}
	s.blackPixel, s.whitePixel = screen.BlackPixel, screen.WhitePixel

	// Listen for resizes and destruction. Several clients may select these
	// on the same window, so this does not disturb the window's owner.
	return xproto.ChangeWindowAttributesChecked(s.conn, win, xproto.CwEventMask,
		[]uint32{uint32(attrs.YourEventMask) |
			xproto.EventMaskStructureNotify | xproto.EventMaskExposure}).Check()
}

func (s *Surface) atom(name string) xproto.Atom {
	reply, err := xproto.InternAtom(s.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return reply.Atom
}
