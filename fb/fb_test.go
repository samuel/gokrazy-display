package fb

import (
	"strconv"
	"testing"
	"unsafe"
)

func TestStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(VarScreenInfo{}); got != 160 {
		t.Errorf("sizeof(VarScreenInfo) = %d, want 160", got)
	}
	want := uintptr(68)
	if strconv.IntSize == 64 {
		want = 80
	}
	if got := unsafe.Sizeof(FixScreenInfo{}); got != want {
		t.Errorf("sizeof(FixScreenInfo) = %d, want %d", got, want)
	}
}

func TestPackUnpackRGB565(t *testing.T) {
	red := Bitfield{Offset: 11, Length: 5}
	green := Bitfield{Offset: 5, Length: 6}
	blue := Bitfield{Offset: 0, Length: 5}

	v := pack(0xffff, red) | pack(0x8000, green) | pack(0, blue)
	if v != 0xfc00 {
		t.Fatalf("pack = %#x, want 0xfc00", v)
	}
	if got := unpack(v, red); got != 0xff {
		t.Errorf("unpack red = %#x, want 0xff", got)
	}
	if got := unpack(v, green); got != 0x81 {
		t.Errorf("unpack green = %#x, want 0x81", got)
	}
	if got := unpack(v, blue); got != 0 {
		t.Errorf("unpack blue = %#x, want 0", got)
	}
}
