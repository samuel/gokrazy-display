package touch

import (
	"image"
	"strconv"
	"testing"
	"unsafe"
)

func TestInputEventSize(t *testing.T) {
	want := uintptr(16)
	if strconv.IntSize == 64 {
		want = 24
	}
	if got := unsafe.Sizeof(inputEvent{}); got != want {
		t.Errorf("sizeof(inputEvent) = %d, want %d", got, want)
	}
}

func TestScale(t *testing.T) {
	d := &Device{X: Range{Min: 0, Max: 319}, Y: Range{Min: 0, Max: 239}}
	for _, tc := range []struct {
		ev   Event
		want image.Point
	}{
		{Event{X: 0, Y: 0}, image.Pt(0, 0)},
		{Event{X: 319, Y: 239}, image.Pt(319, 239)},
		{Event{X: 160, Y: 120}, image.Pt(160, 120)},
	} {
		if got := d.Scale(tc.ev, 320, 240); got != tc.want {
			t.Errorf("Scale(%+v) = %v, want %v", tc.ev, got, tc.want)
		}
	}
}

func TestIoc(t *testing.T) {
	// EVIOCGABS(ABS_X) from linux/input.h
	if got := ioc(2, 'E', 0x40+absX, unsafe.Sizeof(absInfo{})); got != 0x80184540 {
		t.Errorf("EVIOCGABS(ABS_X) = %#x, want 0x80184540", got)
	}
}
