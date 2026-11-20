// Package niri follows niri's window layout through its IPC event stream
// ($NIRI_SOCKET), so a wallpaper knows when windows may have moved over or
// off it, and when the overview is open.
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
// position. Only changes to it matter here.
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
// care about changed. Title changes, which come often, do not count. Focus
// changes do: moving focus to another column scrolls the view, and niri
// reports nothing else when it does.
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
		WindowFocusChanged           *json.RawMessage
		WorkspaceActiveWindowChanged *json.RawMessage
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
	case ev.WindowFocusChanged != nil, ev.WorkspaceActiveWindowChanged != nil:
		return true, nil
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
