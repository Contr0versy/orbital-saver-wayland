package power

import (
	"os"
	"path/filepath"
	"testing"
)

func supply(t *testing.T, root, name string, attrs map[string]string) {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range attrs {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRead(t *testing.T) {
	root := t.TempDir()
	if st := Read(filepath.Join(root, "missing")); !st.OnAC || st.Capacity != -1 {
		t.Errorf("no sysfs: %+v", st)
	}

	supply(t, root, "hidpp_battery_0", map[string]string{"type": "Battery", "scope": "Device", "capacity": "5", "status": "Discharging"})
	if st := Read(root); !st.OnAC || st.Capacity != -1 {
		t.Errorf("only a mouse battery: %+v", st)
	}

	supply(t, root, "BAT1", map[string]string{"type": "Battery", "capacity": "64", "status": "Discharging"})
	if st := Read(root); st.OnAC || st.Capacity != 64 {
		t.Errorf("battery, no mains entry: %+v", st)
	}

	supply(t, root, "ACAD", map[string]string{"type": "Mains", "online": "1"})
	if st := Read(root); !st.OnAC || st.Capacity != 64 {
		t.Errorf("mains online: %+v", st)
	}
	supply(t, root, "ACAD", map[string]string{"online": "0"})
	if st := Read(root); st.OnAC {
		t.Errorf("mains offline: %+v", st)
	}
}
