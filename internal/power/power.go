// Package power reports whether the machine runs on mains or battery, and
// notices when that changes without polling: the kernel announces every
// power-supply change as a uevent on a netlink socket, and the watcher
// sleeps on that socket until one arrives.
package power

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// SysfsRoot is where the kernel lists power supplies.
const SysfsRoot = "/sys/class/power_supply"

// State is a snapshot of the power supplies.
type State struct {
	OnAC     bool
	Capacity int // battery charge in percent, -1 if there is no battery
}

// Read inspects the power supplies under root. A machine with no battery
// counts as on AC.
func Read(root string) State {
	st := State{Capacity: -1}
	entries, err := os.ReadDir(root)
	if err != nil {
		return State{OnAC: true, Capacity: -1}
	}
	mains, mainsOnline, discharging := false, false, false
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		switch readString(dir, "type") {
		case "Mains", "USB":
			mains = true
			if readString(dir, "online") == "1" {
				mainsOnline = true
			}
		case "Battery":
			// Peripherals (mice, headsets) report as batteries too, but
			// with scope Device. Only the system battery matters here.
			if readString(dir, "scope") == "Device" {
				continue
			}
			if st.Capacity < 0 {
				if c, err := strconv.Atoi(readString(dir, "capacity")); err == nil {
					st.Capacity = c
				}
			}
			if readString(dir, "status") == "Discharging" {
				discharging = true
			}
		}
	}
	switch {
	case mains:
		st.OnAC = mainsOnline
	case st.Capacity >= 0:
		st.OnAC = !discharging
	default:
		st.OnAC = true
	}
	return st
}

func readString(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Watch sends the state of the power supplies under root every time it
// changes. The channel holds only the latest state; a slow reader skips
// intermediate ones.
func Watch(root string) (<-chan State, error) {
	const netlinkKobjectUevent = 15
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, netlinkKobjectUevent)
	if err != nil {
		return nil, err
	}
	// Group 1 is the kernel's own uevent broadcast; listening needs no
	// privileges.
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 1}); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	ch := make(chan State, 1)
	last := Read(root)
	go func() {
		defer syscall.Close(fd)
		buf := make([]byte, 8192)
		for {
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				return
			}
			if !bytes.Contains(buf[:n], []byte("\x00SUBSYSTEM=power_supply\x00")) {
				continue
			}
			st := Read(root)
			if st == last {
				continue
			}
			last = st
			select {
			case <-ch: // drop the stale state
			default:
			}
			ch <- st
		}
	}()
	return ch, nil
}
