package wayland

import "fmt"

// dispatch handles one event. Called with s.mu held (or before the event
// goroutine starts). Events for objects we do not track are ignored.
func (s *Surface) dispatch(m *message) {
	switch id := m.sender; {
	case id == 1:
		s.displayEvent(m)
	case id == s.registry:
		s.registryEvent(m)
	case id == s.syncCB && id != 0:
		s.syncFinished = true
		s.syncCB = 0
	case id == s.frameCB && id != 0:
		s.frameCB = 0
		s.pending.Frame = true
		s.wake()
	case id == s.layerSurface && id != 0:
		s.layerSurfaceEvent(m)
	case id == s.surface && id != 0:
		s.surfaceEvent(m)
	case id == s.bufs[0].id && id != 0:
		s.bufs[0].busy = false // wl_buffer.release
	case id == s.bufs[1].id && id != 0:
		s.bufs[1].busy = false
	case s.capt != nil && id == s.capt.frame:
		s.captureEvent(m)
	case id == s.dmabuf && id != 0:
		// modifier(format, modifier_hi, modifier_lo)
		if m.opcode == 1 && m.uint(0) == drmFormatXRGB8888 && m.uint(1) == 0 && m.uint(2) == drmModLinear {
			s.dmabufLinear = true
		}
	default:
		for _, o := range s.outputs {
			if o.id == id {
				s.outputEvent(o, m)
				return
			}
		}
	}
}

func (s *Surface) displayEvent(m *message) {
	switch m.opcode {
	case 0: // error(object, code, message)
		msg, _ := m.string(8)
		s.quit(fmt.Errorf("Wayland protocol error on object %d (code %d): %s", m.uint(0), m.uint(1), msg))
	case 1: // delete_id
		s.c.releaseID(m.uint(0))
	}
}

func (s *Surface) registryEvent(m *message) {
	switch m.opcode {
	case 0: // global(name, interface, version)
		name := m.uint(0)
		iface, off := m.string(4)
		version := uint32(0)
		if len(m.args) >= off+4 {
			version = order.Uint32(m.args[off:])
		}
		switch iface {
		case "wl_compositor":
			s.compositorVer = minU32(version, compositorVersion)
			s.compositor = s.bind(name, iface, s.compositorVer)
		case "wl_shm":
			s.shm = s.bind(name, iface, minU32(version, shmVersion))
		case "zwlr_layer_shell_v1":
			s.layerShell = s.bind(name, iface, minU32(version, layerShellVersion))
		case "wl_output":
			v := minU32(version, outputVersion)
			o := &output{name: name, version: v, outputName: fmt.Sprintf("output-%d", name)}
			o.id = s.bind(name, iface, v)
			s.outputs = append(s.outputs, o)
		case "zwp_linux_dmabuf_v1":
			if s.cfg.DMABuf && version >= dmabufVersion {
				s.dmabuf = s.bind(name, iface, dmabufVersion)
			}
		case "zwlr_screencopy_manager_v1":
			s.screencopyVer = minU32(version, screencopyVersion)
			s.screencopy = s.bind(name, iface, s.screencopyVer)
		}
	case 1: // global_remove(name)
		name := m.uint(0)
		for i, o := range s.outputs {
			if o.name != name {
				continue
			}
			if o.version >= 3 {
				s.c.request(o.id, 0) // release
			}
			if s.entered == o {
				s.entered = nil
			}
			if s.target == o {
				// Our output was unplugged. The compositor will close the
				// layer surface too; quitting here makes sure of it.
				s.quit(nil)
			}
			s.outputs = append(s.outputs[:i], s.outputs[i+1:]...)
			return
		}
	}
}

// bind creates a client object for a global.
func (s *Surface) bind(name uint32, iface string, version uint32) uint32 {
	id := s.c.newID()
	start := s.c.begin(s.registry, 0)
	s.c.putUint(name)
	s.c.putString(iface)
	s.c.putUint(version)
	s.c.putUint(id)
	s.c.end(start)
	return id
}

func (s *Surface) outputEvent(o *output, m *message) {
	switch m.opcode {
	case 1: // mode(flags, width, height, refresh)
		if m.uint(0)&1 != 0 { // current
			o.width, o.height, o.refresh = int(m.int(1)), int(m.int(2)), int(m.int(3))
			s.pending.Configure = true
			s.wake()
		}
	case 3: // scale
		o.scale = int(m.int(0))
		s.pending.Configure = true
		s.wake()
	case 4: // name
		o.outputName, _ = m.string(0)
	}
}

func (s *Surface) layerSurfaceEvent(m *message) {
	switch m.opcode {
	case 0: // configure(serial, width, height)
		s.pendingSerial = m.uint(0)
		s.pendingW, s.pendingH = int(m.uint(1)), int(m.uint(2))
		s.pending.Configure = true
		s.wake()
	case 1: // closed
		s.quit(nil)
	}
}

func (s *Surface) surfaceEvent(m *message) {
	switch m.opcode {
	case 0: // enter(output)
		id := m.uint(0)
		for _, o := range s.outputs {
			if o.id == id {
				s.entered = o
			}
		}
		s.pending.Configure = true
		s.wake()
	case 1: // leave(output)
		if s.entered != nil && s.entered.id == m.uint(0) {
			s.entered = nil
		}
	case 2: // preferred_buffer_scale
		s.preferredScale = int(m.int(0))
		s.pending.Configure = true
		s.wake()
	}
}

func minU32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
