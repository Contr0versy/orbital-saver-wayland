// Package niri follows niri's window layout through its IPC event stream,
// so a wallpaper can tell how much of itself is showing between windows.
//
// Wayland does not tell a client which of its pixels are covered. niri's
// IPC socket ($NIRI_SOCKET) does say which workspace is active on each
// output and how big every window's tile is, which is enough to estimate it.
package niri

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

// Workspace is the part of niri's workspace description we use.
type Workspace struct {
	ID       uint64 `json:"id"`
	Output   string `json:"output"`
	IsActive bool   `json:"is_active"`
}

// Layout is where a window sits. Tiled windows have a column and row in
// the scrolling layout but no position on screen; floating windows have a
// position.
type Layout struct {
	Column   *[2]int     `json:"pos_in_scrolling_layout"` // column, row; 1-based
	TileSize [2]float64  `json:"tile_size"`               // logical pixels
	TilePos  *[2]float64 `json:"tile_pos_in_workspace_view"`
}

// Window is the part of niri's window description we use.
type Window struct {
	ID          uint64  `json:"id"`
	WorkspaceID *uint64 `json:"workspace_id"`
	IsFloating  bool    `json:"is_floating"`
	Layout      Layout  `json:"layout"`
}

// State is niri's layout as last reported.
type State struct {
	Overview   bool
	Workspaces map[uint64]Workspace
	Windows    map[uint64]Window
}

// Visible estimates the fraction of a w×h (logical pixels) wallpaper on
// output that is not covered by windows: 1 with the overview open or on an
// empty workspace, near 0 under a fullscreen or maximized window.
//
// Tiled windows have no reported position, so columns are assumed to be
// on screen side by side; when they are wider than the screen together
// they are assumed to fill it. Windows are taken to be opaque.
func (s *State) Visible(output string, w, h float64) float64 {
	if s.Overview || w <= 0 || h <= 0 {
		return 1
	}
	var active uint64
	found := false
	for _, ws := range s.Workspaces {
		if ws.IsActive && (ws.Output == output || output == "") {
			active, found = ws.ID, true
			break
		}
	}
	if !found {
		return 1
	}
	type column struct{ w, h float64 }
	columns := map[int]*column{}
	covered := 0.0
	for _, win := range s.Windows {
		if win.WorkspaceID == nil || *win.WorkspaceID != active {
			continue
		}
		tw, th := win.Layout.TileSize[0], win.Layout.TileSize[1]
		if win.IsFloating || win.Layout.Column == nil {
			if p := win.Layout.TilePos; p != nil {
				tw = clampSpan(p[0], tw, w)
				th = clampSpan(p[1], th, h)
			}
			covered += tw * th
			continue
		}
		c := columns[win.Layout.Column[0]]
		if c == nil {
			c = &column{}
			columns[win.Layout.Column[0]] = c
		}
		if tw > c.w {
			c.w = tw
		}
		c.h += th // stacked windows; tabbed ones overlap and are capped below
	}
	totalW, tiled := 0.0, 0.0
	for _, c := range columns {
		totalW += c.w
		tiled += c.w * minF(c.h, h)
	}
	if totalW > w {
		tiled *= w / totalW
	}
	covered += tiled
	v := 1 - covered/(w*h)
	if v < 0 {
		return 0
	}
	return v
}

// clampSpan is the length of [pos, pos+size) that lies within [0, limit).
func clampSpan(pos, size, limit float64) float64 {
	lo, hi := pos, pos+size
	if lo < 0 {
		lo = 0
	}
	if hi > limit {
		hi = limit
	}
	if hi < lo {
		return 0
	}
	return hi - lo
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (s *State) clone() State {
	c := State{
		Overview:   s.Overview,
		Workspaces: make(map[uint64]Workspace, len(s.Workspaces)),
		Windows:    make(map[uint64]Window, len(s.Windows)),
	}
	for k, v := range s.Workspaces {
		c.Workspaces[k] = v
	}
	for k, v := range s.Windows {
		c.Windows[k] = v
	}
	return c
}

// apply updates the state from one event and reports whether anything we
// care about changed. Title and focus changes, which come often, do not
// count.
func (s *State) apply(line []byte) (changed bool, err error) {
	var ev struct {
		WorkspacesChanged *struct {
			Workspaces []Workspace `json:"workspaces"`
		}
		WorkspaceActivated *struct {
			ID uint64 `json:"id"`
		}
		WindowsChanged *struct {
			Windows []Window `json:"windows"`
		}
		WindowOpenedOrChanged *struct {
			Window Window `json:"window"`
		}
		WindowClosed *struct {
			ID uint64 `json:"id"`
		}
		WindowLayoutsChanged *struct {
			Changes []json.RawMessage `json:"changes"` // [id, layout] pairs
		}
		OverviewOpenedOrClosed *struct {
			IsOpen bool `json:"is_open"`
		}
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return false, err
	}
	switch {
	case ev.WorkspacesChanged != nil:
		s.Workspaces = make(map[uint64]Workspace)
		for _, ws := range ev.WorkspacesChanged.Workspaces {
			s.Workspaces[ws.ID] = ws
		}
		return true, nil
	case ev.WorkspaceActivated != nil:
		on, ok := s.Workspaces[ev.WorkspaceActivated.ID]
		if !ok || on.IsActive {
			return false, nil
		}
		for id, ws := range s.Workspaces {
			if ws.Output == on.Output {
				ws.IsActive = id == on.ID
				s.Workspaces[id] = ws
			}
		}
		return true, nil
	case ev.WindowsChanged != nil:
		s.Windows = make(map[uint64]Window)
		for _, w := range ev.WindowsChanged.Windows {
			s.Windows[w.ID] = w
		}
		return true, nil
	case ev.WindowOpenedOrChanged != nil:
		w := ev.WindowOpenedOrChanged.Window
		old, ok := s.Windows[w.ID]
		s.Windows[w.ID] = w
		return !ok || !sameWindow(old, w), nil
	case ev.WindowClosed != nil:
		_, ok := s.Windows[ev.WindowClosed.ID]
		delete(s.Windows, ev.WindowClosed.ID)
		return ok, nil
	case ev.WindowLayoutsChanged != nil:
		for _, raw := range ev.WindowLayoutsChanged.Changes {
			var pair [2]json.RawMessage
			var id uint64
			var l Layout
			if json.Unmarshal(raw, &pair) != nil || json.Unmarshal(pair[0], &id) != nil || json.Unmarshal(pair[1], &l) != nil {
				continue
			}
			if w, ok := s.Windows[id]; ok {
				w.Layout = l
				s.Windows[id] = w
				changed = true
			}
		}
		return changed, nil
	case ev.OverviewOpenedOrClosed != nil:
		changed = s.Overview != ev.OverviewOpenedOrClosed.IsOpen
		s.Overview = ev.OverviewOpenedOrClosed.IsOpen
		return changed, nil
	}
	return false, nil
}

func sameWindow(a, b Window) bool {
	sameWS := (a.WorkspaceID == nil) == (b.WorkspaceID == nil) &&
		(a.WorkspaceID == nil || *a.WorkspaceID == *b.WorkspaceID)
	return sameWS && a.IsFloating == b.IsFloating && sameLayout(a.Layout, b.Layout)
}

func sameLayout(a, b Layout) bool {
	return a.TileSize == b.TileSize &&
		(a.Column == nil) == (b.Column == nil) && (a.Column == nil || *a.Column == *b.Column) &&
		(a.TilePos == nil) == (b.TilePos == nil) && (a.TilePos == nil || *a.TilePos == *b.TilePos)
}

// Watch connects to niri and sends the layout after every change that
// matters, starting with the full state. Only the latest state is kept
// if the receiver falls behind. The channel closes if niri goes away.
// It fails at once when not running under niri.
func Watch() (<-chan State, error) {
	path := os.Getenv("NIRI_SOCKET")
	if path == "" {
		return nil, errors.New("NIRI_SOCKET is not set: not running under niri")
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte("\"EventStream\"\n")); err != nil {
		conn.Close()
		return nil, err
	}
	r := bufio.NewReaderSize(conn, 1<<16)
	reply, err := r.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	var ok struct{ Ok json.RawMessage }
	if json.Unmarshal(reply, &ok) != nil || ok.Ok == nil {
		conn.Close()
		return nil, fmt.Errorf("niri refused the event stream: %s", reply)
	}

	out := make(chan State, 1)
	go func() {
		defer conn.Close()
		defer close(out)
		st := State{Workspaces: map[uint64]Workspace{}, Windows: map[uint64]Window{}}
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			changed, err := st.apply(line)
			if err != nil || !changed {
				continue
			}
			// Replace an unread state rather than block.
			select {
			case <-out:
			default:
			}
			out <- st.clone()
		}
	}()
	return out, nil
}
