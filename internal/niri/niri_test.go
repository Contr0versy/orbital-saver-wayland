package niri

import (
	"math"
	"testing"
)

func feed(t *testing.T, st *State, events ...string) {
	t.Helper()
	for _, e := range events {
		if _, err := st.apply([]byte(e)); err != nil {
			t.Fatalf("%s: %v", e, err)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

const workspaces = `{"WorkspacesChanged":{"workspaces":[
	{"id":20,"idx":2,"name":null,"output":"eDP-1","is_urgent":false,"is_active":false,"is_focused":false,"active_window_id":null},
	{"id":17,"idx":1,"name":null,"output":"eDP-1","is_urgent":false,"is_active":true,"is_focused":true,"active_window_id":135}]}}`

// One maximized window on workspace 17, as niri reports a foot terminal
// on a 1440x960 screen.
const oneWindow = `{"WindowsChanged":{"windows":[{"id":135,"title":"x","app_id":"foot","pid":1,"workspace_id":17,
	"is_focused":true,"is_floating":false,"is_urgent":false,"layout":{"pos_in_scrolling_layout":[1,1],
	"tile_size":[1416.0,902.0],"window_size":[1415,901],"tile_pos_in_workspace_view":null,"window_offset_in_tile":[0.5,0.5]}}]}}`

func newState() *State {
	return &State{Workspaces: map[uint64]Workspace{}, Windows: map[uint64]Window{}}
}

func TestVisible(t *testing.T) {
	st := newState()
	feed(t, st, workspaces, oneWindow)
	if v := st.Visible("eDP-1", 1440, 960); !near(v, 1-1416.0*902/(1440*960)) {
		t.Errorf("one maximized window: visible %.3f", v)
	}

	// Switching to the empty workspace shows everything.
	feed(t, st, `{"WorkspaceActivated":{"id":20,"focused":true}}`)
	if v := st.Visible("eDP-1", 1440, 960); v != 1 {
		t.Errorf("empty workspace: visible %.3f", v)
	}
	feed(t, st, `{"WorkspaceActivated":{"id":17,"focused":true}}`)

	// So does the overview.
	feed(t, st, `{"OverviewOpenedOrClosed":{"is_open":true}}`)
	if v := st.Visible("eDP-1", 1440, 960); v != 1 {
		t.Errorf("overview: visible %.3f", v)
	}
	feed(t, st, `{"OverviewOpenedOrClosed":{"is_open":false}}`)

	// A half-width column leaves about half the screen.
	feed(t, st, `{"WindowLayoutsChanged":{"changes":[[135,{"pos_in_scrolling_layout":[1,1],"tile_size":[708.0,902.0],
		"window_size":[707,901],"tile_pos_in_workspace_view":null,"window_offset_in_tile":[0.5,0.5]}]]}}`)
	if v := st.Visible("eDP-1", 1440, 960); !near(v, 1-708.0*902/(1440*960)) {
		t.Errorf("half-width column: visible %.3f", v)
	}

	// Three such columns are wider than the screen, so they fill it.
	for _, id := range []string{"136", "137"} {
		col := map[string]string{"136": "2", "137": "3"}[id]
		feed(t, st, `{"WindowOpenedOrChanged":{"window":{"id":`+id+`,"workspace_id":17,"is_floating":false,
			"layout":{"pos_in_scrolling_layout":[`+col+`,1],"tile_size":[708.0,902.0],"tile_pos_in_workspace_view":null}}}}`)
	}
	if v := st.Visible("eDP-1", 1440, 960); !near(v, 1-902.0/960) {
		t.Errorf("columns wider than the screen: visible %.3f", v)
	}

	// Closing them gets back to one column; another output is unaffected.
	feed(t, st, `{"WindowClosed":{"id":136}}`, `{"WindowClosed":{"id":137}}`)
	if v := st.Visible("HDMI-A-1", 1920, 1080); v != 1 {
		t.Errorf("other output: visible %.3f", v)
	}
}

func TestFloatingWindowIsClipped(t *testing.T) {
	st := newState()
	feed(t, st, workspaces, `{"WindowsChanged":{"windows":[{"id":1,"workspace_id":17,"is_floating":true,
		"layout":{"pos_in_scrolling_layout":null,"tile_size":[400.0,300.0],"tile_pos_in_workspace_view":[1240.0,-100.0]}}]}}`)
	// 200x200 of it is on screen.
	if v := st.Visible("eDP-1", 1440, 960); !near(v, 1-200.0*200/(1440*960)) {
		t.Errorf("visible %.3f", v)
	}
}

func TestTitleChangesAreIgnored(t *testing.T) {
	st := newState()
	feed(t, st, workspaces, oneWindow)
	changed, err := st.apply([]byte(`{"WindowOpenedOrChanged":{"window":{"id":135,"title":"y","app_id":"foot","pid":1,
		"workspace_id":17,"is_focused":true,"is_floating":false,"is_urgent":false,"layout":{"pos_in_scrolling_layout":[1,1],
		"tile_size":[1416.0,902.0],"window_size":[1415,901],"tile_pos_in_workspace_view":null,"window_offset_in_tile":[0.5,0.5]}}}}`))
	if err != nil || changed {
		t.Errorf("a title change counted as a layout change (err %v)", err)
	}
}
