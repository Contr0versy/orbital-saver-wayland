package wayland

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// The Wayland wire format: every message is a header of two 32-bit words
// in host byte order (the object id, then size<<16 | opcode) followed by
// its arguments, each padded to 32 bits. File descriptors travel out of
// band as SCM_RIGHTS.
var order = binary.LittleEndian

const headerSize = 8

// conn is a Wayland connection. Requests are buffered in out and sent in
// one write by flush, so a whole frame costs one system call.
type conn struct {
	sock *net.UnixConn

	out    []byte
	outFDs []int

	in       []byte // received bytes; in[inHead:] is not yet dispatched
	inHead   int
	oob      []byte
	nextID   uint32
	freeIDs  []uint32
	readErr  error
	scratchM message
}

// message is one received event. args aliases the receive buffer and is
// only valid until the next read.
type message struct {
	sender uint32
	opcode uint16
	args   []byte
}

// dial connects to the compositor named by $WAYLAND_SOCKET or
// $WAYLAND_DISPLAY, as libwayland does.
func dial() (*conn, error) {
	var sock *net.UnixConn
	if fdStr := os.Getenv("WAYLAND_SOCKET"); fdStr != "" {
		fd, err := strconv.Atoi(fdStr)
		if err != nil {
			return nil, fmt.Errorf("invalid WAYLAND_SOCKET %q", fdStr)
		}
		os.Unsetenv("WAYLAND_SOCKET")
		f := os.NewFile(uintptr(fd), "wayland")
		c, err := net.FileConn(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		u, ok := c.(*net.UnixConn)
		if !ok {
			c.Close()
			return nil, errors.New("WAYLAND_SOCKET is not a Unix socket")
		}
		sock = u
	} else {
		name := os.Getenv("WAYLAND_DISPLAY")
		if name == "" {
			name = "wayland-0"
		}
		path := name
		if !filepath.IsAbs(path) {
			dir := os.Getenv("XDG_RUNTIME_DIR")
			if dir == "" {
				return nil, errors.New("XDG_RUNTIME_DIR is not set")
			}
			path = filepath.Join(dir, name)
		}
		var err error
		sock, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			return nil, err
		}
	}
	return &conn{
		sock:   sock,
		in:     make([]byte, 0, 1<<16),
		oob:    make([]byte, syscall.CmsgSpace(28*4)), // libwayland sends at most 28 fds at once
		nextID: 2,                                     // 1 is wl_display
	}, nil
}

// newID allocates a client object id, reusing ids the server has released.
func (c *conn) newID() uint32 {
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id
	}
	id := c.nextID
	c.nextID++
	return id
}

func (c *conn) releaseID(id uint32) { c.freeIDs = append(c.freeIDs, id) }

// Request encoding. begin writes a header with a placeholder size, the
// argument writers append, and end patches the size in.

func (c *conn) begin(object uint32, opcode uint16) int {
	start := len(c.out)
	c.out = order.AppendUint32(c.out, object)
	c.out = order.AppendUint32(c.out, uint32(opcode))
	return start
}

func (c *conn) end(start int) {
	size := uint32(len(c.out) - start)
	order.PutUint32(c.out[start+4:], size<<16|order.Uint32(c.out[start+4:])&0xffff)
}

func (c *conn) putUint(v uint32) { c.out = order.AppendUint32(c.out, v) }
func (c *conn) putInt(v int32)   { c.out = order.AppendUint32(c.out, uint32(v)) }

func (c *conn) putString(s string) {
	c.putUint(uint32(len(s) + 1))
	c.out = append(c.out, s...)
	c.out = append(c.out, 0)
	for len(c.out)%4 != 0 {
		c.out = append(c.out, 0)
	}
}

// putFD queues a file descriptor to go with the next flush. The caller
// keeps ownership and may close it once flush returns.
func (c *conn) putFD(fd int) { c.outFDs = append(c.outFDs, fd) }

// request writes a message whose arguments are all 32-bit words.
func (c *conn) request(object uint32, opcode uint16, args ...uint32) {
	start := c.begin(object, opcode)
	for _, a := range args {
		c.putUint(a)
	}
	c.end(start)
}

// flush sends every buffered request.
func (c *conn) flush() error {
	if len(c.out) == 0 {
		return nil
	}
	var err error
	if len(c.outFDs) > 0 {
		_, _, err = c.sock.WriteMsgUnix(c.out, syscall.UnixRights(c.outFDs...), nil)
		c.outFDs = c.outFDs[:0]
	} else {
		_, err = c.sock.Write(c.out)
	}
	c.out = c.out[:0]
	return err
}

// read blocks until more bytes arrive. File descriptors the compositor
// sends (only keymaps, for what we bind) are closed straight away: nothing
// here needs them.
func (c *conn) read() error {
	if c.inHead > 0 {
		n := copy(c.in[:cap(c.in)], c.in[c.inHead:])
		c.in, c.inHead = c.in[:n], 0
	}
	if len(c.in) == cap(c.in) {
		return errors.New("wayland: message larger than receive buffer")
	}
	n, oobn, _, _, err := c.sock.ReadMsgUnix(c.in[len(c.in):cap(c.in)], c.oob)
	if oobn > 0 {
		closeRights(c.oob[:oobn])
	}
	if n <= 0 {
		if err == nil {
			err = errors.New("compositor closed the connection")
		}
		return err
	}
	c.in = c.in[:len(c.in)+n]
	return err
}

func closeRights(oob []byte) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for i := range msgs {
		if fds, err := syscall.ParseUnixRights(&msgs[i]); err == nil {
			for _, fd := range fds {
				syscall.Close(fd)
			}
		}
	}
}

// next returns the next complete message in the receive buffer, if any.
func (c *conn) next() (*message, bool) {
	buf := c.in[c.inHead:]
	if len(buf) < headerSize {
		return nil, false
	}
	word := order.Uint32(buf[4:])
	size := int(word >> 16)
	if size < headerSize {
		c.readErr = fmt.Errorf("wayland: malformed message of %d bytes", size)
		return nil, false
	}
	if len(buf) < size {
		return nil, false
	}
	c.inHead += size
	c.scratchM = message{sender: order.Uint32(buf), opcode: uint16(word), args: buf[headerSize:size]}
	return &c.scratchM, true
}

func (c *conn) close() { c.sock.Close() }

// Argument decoding. A short message yields zeros rather than a panic; a
// compositor that sends one has bigger problems.

func (m *message) uint(i int) uint32 {
	if len(m.args) < 4*(i+1) {
		return 0
	}
	return order.Uint32(m.args[4*i:])
}

func (m *message) int(i int) int32 { return int32(m.uint(i)) }

// string decodes a string argument starting at byte offset off and returns
// it with the offset of the next argument.
func (m *message) string(off int) (string, int) {
	if len(m.args) < off+4 {
		return "", len(m.args)
	}
	n := int(order.Uint32(m.args[off:]))
	off += 4
	if n == 0 || len(m.args) < off+n {
		return "", off
	}
	s := string(m.args[off : off+n-1]) // drop the NUL
	return s, off + (n+3)&^3
}
