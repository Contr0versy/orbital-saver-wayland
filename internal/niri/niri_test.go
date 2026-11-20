package niri

import "testing"

func feed(t *testing.T, st *State, events ...string) {
	t.Helper()
	for _, e := range events {
		if _, err := st.apply([]byte(e)); err != nil {
			t.Fatalf("%s: %v", e, err)
		}
	}
}

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

func TestFocusChangesCount(t *testing.T) {
	st := newState()
	feed(t, st, workspaces, oneWindow)
	// Focusing another column scrolls the view; nothing else is reported.
	for _, e := range []string{
		`{"WindowFocusChanged":{"id":135}}`,
		`{"WindowFocusChanged":{"id":null}}`,
		`{"WorkspaceActiveWindowChanged":{"workspace_id":17,"active_window_id":135}}`,
	} {
		if changed, err := st.apply([]byte(e)); err != nil || !changed {
			t.Errorf("%s: changed = %v, err %v; want a change", e, changed, err)
		}
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
