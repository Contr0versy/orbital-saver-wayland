package x11

import "testing"

func TestParseWindowID(t *testing.T) {
	good := map[string]uint32{
		"0x3a00007":  0x3a00007,
		"0X3A00007":  0x3a00007,
		"60817415":   60817415,
		" 0x1e0000c": 0x1e0000c,
		"0xffffffff": 0xffffffff,
	}
	for in, want := range good {
		got, err := ParseWindowID(in)
		if err != nil || got != want {
			t.Errorf("ParseWindowID(%q) = %#x, %v; want %#x", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0x0", "window", "-5", "0x1ffffffff", "12.5"} {
		if got, err := ParseWindowID(in); err == nil {
			t.Errorf("ParseWindowID(%q) = %#x, want an error", in, got)
		}
	}
}

func TestChannelScaling(t *testing.T) {
	cases := []struct {
		v    uint8
		mask uint32
		want uint32
	}{
		{255, 0xff0000, 0xff0000}, // 8-bit channel
		{128, 0x00ff00, 0x008000},
		{255, 0xf800, 0xf800},         // 5-bit channel (RGB565)
		{255, 0x3ff00000, 0x3ff00000}, // 10-bit channel (depth 30)
		{0, 0xff, 0},
		{200, 0, 0}, // visual without this channel
	}
	for _, c := range cases {
		if got := channel(c.v, c.mask); got != c.want {
			t.Errorf("channel(%d, %#x) = %#x, want %#x", c.v, c.mask, got, c.want)
		}
	}
}
