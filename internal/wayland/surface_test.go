package wayland

import (
	"image/color"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStringEncoding(t *testing.T) {
	c := &conn{}
	start := c.begin(5, 3)
	c.putString("abc") // 4 bytes with NUL: no padding
	c.putString("abcd")
	c.putUint(9)
	c.end(start)
	if len(c.out) != 8+4+4+4+8+4 {
		t.Fatalf("encoded %d bytes", len(c.out))
	}
	c.in = c.out
	m, ok := c.next()
	if !ok || m.sender != 5 || m.opcode != 3 {
		t.Fatalf("decoded header %+v, %v", m, ok)
	}
	s1, off := m.string(0)
	s2, off := m.string(off)
	if s1 != "abc" || s2 != "abcd" || order.Uint32(m.args[off:]) != 9 {
		t.Errorf("decoded %q %q", s1, s2)
	}
}

// fakeCompositor answers just enough of the protocol to drive a Surface:
// one 2880x1920 output at scale 2 and 120 Hz, and a layer surface that is
// configured to 1440x960 logical pixels.
type fakeCompositor struct {
	t *testing.T
	c *conn

	ids      map[uint32]string // client object id -> interface
	surface  uint32
	layer    uint32
	frame    uint32
	commits  chan []string // the requests leading up to each commit
	log      []string
	attached uint32
}

func startFake(t *testing.T) *fakeCompositor {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fds[0]), "server")
	sc, err := net.FileConn(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	// The client end is handed over the way a compositor launching a
	// client would.
	t.Setenv("WAYLAND_SOCKET", strconv.Itoa(fds[1]))
	fc := &fakeCompositor{
		t:       t,
		c:       &conn{sock: sc.(*net.UnixConn), in: make([]byte, 0, 1<<16), oob: make([]byte, 256)},
		ids:     map[uint32]string{1: "wl_display"},
		commits: make(chan []string, 16),
	}
	go fc.serve()
	return fc
}

func (f *fakeCompositor) serve() {
	for {
		if err := f.c.read(); err != nil {
			close(f.commits)
			return
		}
		for {
			m, ok := f.c.next()
			if !ok {
				break
			}
			f.handle(m)
		}
		f.c.flush()
	}
}

func (f *fakeCompositor) event(obj uint32, op uint16, args ...uint32) {
	f.c.request(obj, op, args...)
}

func (f *fakeCompositor) global(registry, name uint32, iface string, version uint32) {
	start := f.c.begin(registry, 0)
	f.c.putUint(name)
	f.c.putString(iface)
	f.c.putUint(version)
	f.c.end(start)
}

func (f *fakeCompositor) handle(m *message) {
	iface := f.ids[m.sender]
	f.log = append(f.log, iface+"."+strconv.Itoa(int(m.opcode)))
	switch iface {
	case "wl_display":
		switch m.opcode {
		case 0: // sync
			f.event(m.uint(0), 0, 0)
			f.event(1, 1, m.uint(0)) // delete_id
		case 1: // get_registry
			r := m.uint(0)
			f.ids[r] = "wl_registry"
			f.global(r, 1, "wl_compositor", 6)
			f.global(r, 2, "wl_shm", 1)
			f.global(r, 3, "zwlr_layer_shell_v1", 4)
			f.global(r, 4, "wl_output", 4)
		}
	case "wl_registry": // bind
		iface, off := m.string(4)
		id := order.Uint32(m.args[off+4:])
		f.ids[id] = iface
		if iface == "wl_output" {
			f.event(id, 1, 1, 2880, 1920, 120000) // mode: current
			f.event(id, 3, 2)                     // scale
			start := f.c.begin(id, 4)             // name
			f.c.putString("eDP-1")
			f.c.end(start)
			f.event(id, 2) // done
		}
	case "wl_compositor":
		if m.opcode == 0 {
			f.ids[m.uint(0)] = "wl_surface"
			f.surface = m.uint(0)
		} else {
			f.ids[m.uint(0)] = "wl_region"
		}
	case "zwlr_layer_shell_v1":
		f.ids[m.uint(0)] = "zwlr_layer_surface_v1"
		f.layer = m.uint(0)
	case "wl_shm":
		f.ids[m.uint(0)] = "wl_shm_pool"
		if size := m.int(1); size != 2*2880*1920*4 {
			f.t.Errorf("pool size = %d, want two 2880x1920 buffers", size)
		}
	case "wl_shm_pool":
		if m.opcode == 0 {
			f.ids[m.uint(0)] = "wl_buffer"
			if w, h := m.int(2), m.int(3); w != 2880 || h != 1920 {
				f.t.Errorf("buffer is %dx%d, want 2880x1920", w, h)
			}
		}
	case "wl_surface":
		switch m.opcode {
		case 1:
			f.attached = m.uint(0)
		case 3:
			f.frame = m.uint(0)
			f.ids[f.frame] = "wl_callback"
		case 6: // commit
			if f.attached == 0 {
				f.event(f.layer, 0, 7, 1440, 960) // configure
			} else {
				f.event(f.surface, 0, 4) // enter the output
				if f.frame != 0 {
					f.event(f.frame, 0, 0)
					f.event(1, 1, f.frame)
					f.frame = 0
				}
				f.event(f.attached, 0) // release
				f.attached = 0
			}
			f.commits <- f.log
			f.log = nil
		}
	}
}

func waitFor(t *testing.T, s *Surface, want func(Pending) bool) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case <-s.Notify():
			p := s.Pending()
			if p.Quit {
				t.Fatalf("surface quit: %v", p.Err)
			}
			if want(p) {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for the compositor")
		}
	}
}

func TestSurfaceAgainstFakeCompositor(t *testing.T) {
	fake := startFake(t)
	s, err := Open(Config{
		Layer:      LayerBackground,
		Namespace:  "orbital-saver",
		Output:     "eDP-1",
		Background: color.RGBA{A: 255},
		Ramp:       []color.RGBA{{R: 255, A: 255}},
		DMABuf:     true, // the fake offers no zwp_linux_dmabuf_v1: falls back
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	<-fake.commits // the initial, bufferless commit

	var w, h int
	ok := false
	waitFor(t, s, func(p Pending) bool {
		// Output events also report Configure; only the layer surface's
		// configure makes Begin succeed.
		w, h, ok = s.Begin()
		return ok
	})
	if w != 2880 || h != 1920 {
		t.Fatalf("Begin = %d, %d, %v; want 2880, 1920, true", w, h, ok)
	}
	if r := s.Refresh(); r != 120000 {
		t.Errorf("Refresh = %d, want 120000", r)
	}
	s.Line(0, 10, 10, 500, 300)
	if committed, err := s.Present(); !committed || err != nil {
		t.Fatalf("Present = %v, %v", committed, err)
	}
	log := <-fake.commits
	want := map[string]bool{
		"zwlr_layer_surface_v1.6": true, // ack_configure
		"wl_surface.8":            true, // set_buffer_scale
		"wl_surface.4":            true, // set_opaque_region
		"wl_surface.1":            true, // attach
		"wl_surface.9":            true, // damage_buffer
		"wl_surface.3":            true, // frame
	}
	for _, r := range log {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("missing requests %v in %v", want, log)
	}
	waitFor(t, s, func(p Pending) bool { return p.Frame })
	if b := s.Buffers(); !strings.HasPrefix(b, "shared memory (no dma-bufs") {
		t.Errorf("Buffers = %q, want a fallback to shared memory", b)
	}

	// The same picture again: nothing to commit.
	s.Begin()
	s.Line(0, 10, 10, 500, 300)
	if committed, err := s.Present(); committed || err != nil {
		t.Errorf("unchanged frame: Present = %v, %v; want no commit", committed, err)
	}
}

func TestUnknownOutput(t *testing.T) {
	startFake(t)
	_, err := Open(Config{Output: "HDMI-A-9", Ramp: []color.RGBA{{}}})
	if err == nil || err.Error() != `no output named "HDMI-A-9" (available: eDP-1)` {
		t.Errorf("err = %v", err)
	}
}
