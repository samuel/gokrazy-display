// Package touch reads single-touch events from a Linux evdev touchscreen
// (/dev/input/eventN).
package touch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Event types and codes from linux/input-event-codes.h.
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport = 0x00
	btnTouch  = 0x14a
	absX      = 0x00
	absY      = 0x01
)

// inputEvent mirrors struct input_event. unix.Timeval has the kernel's
// layout on both 32 and 64-bit ARM.
type inputEvent struct {
	Time  unix.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

// absInfo mirrors struct input_absinfo.
type absInfo struct {
	Value      int32
	Minimum    int32
	Maximum    int32
	Fuzz       int32
	Flat       int32
	Resolution int32
}

// Range is the reported range of an axis.
type Range struct {
	Min, Max int32
}

// Event is the touch state at a SYN_REPORT.
type Event struct {
	X, Y int32 // raw axis values
	Down bool
}

// Device is an evdev touchscreen.
type Device struct {
	file *os.File
	path string
	name string
	X, Y Range

	state Event
}

// ErrNotFound is returned by Find when no touchscreen is present.
var ErrNotFound = errors.New("no touchscreen found")

// Find opens the first input device that reports ABS_X, ABS_Y and BTN_TOUCH.
func Find() (*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		d, err := Open(p)
		if err != nil {
			continue
		}
		if d.isTouchscreen() {
			return d, nil
		}
		d.Close() //nolint:errcheck // probing only, nothing was written
	}
	return nil, ErrNotFound
}

// Open opens an input device by path.
func Open(path string) (*Device, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	d := &Device{file: f, path: path}

	name := make([]byte, 256)
	if err := d.ioctl(ioc(2, 'E', 0x06, uintptr(len(name))), unsafe.Pointer(&name[0])); err == nil {
		n, _, _ := bytes.Cut(name, []byte{0})
		d.name = string(n)
	}
	if d.X, err = d.absRange(absX); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("EVIOCGABS(ABS_X): %w", err)
	}
	if d.Y, err = d.absRange(absY); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("EVIOCGABS(ABS_Y): %w", err)
	}
	return d, nil
}

// Path returns the device node path.
func (d *Device) Path() string { return d.path }

// Name returns the device name reported by the driver.
func (d *Device) Name() string { return d.name }

// Close closes the device.
func (d *Device) Close() error { return d.file.Close() }

// Read blocks until the next SYN_REPORT and returns the touch state.
func (d *Device) Read() (Event, error) {
	var ev inputEvent
	buf := make([]byte, unsafe.Sizeof(ev))
	for {
		if _, err := io.ReadFull(d.file, buf); err != nil {
			return Event{}, err
		}
		if _, err := binary.Decode(buf, binary.NativeEndian, &ev); err != nil {
			return Event{}, err
		}
		switch ev.Type {
		case evAbs:
			switch ev.Code {
			case absX:
				d.state.X = ev.Value
			case absY:
				d.state.Y = ev.Value
			}
		case evKey:
			if ev.Code == btnTouch {
				d.state.Down = ev.Value != 0
			}
		case evSyn:
			if ev.Code == synReport {
				return d.state, nil
			}
		}
	}
}

// Scale maps the raw coordinates of e onto an area of the given size.
func (d *Device) Scale(e Event, width, height int) image.Point {
	return image.Pt(scale(e.X, d.X, width), scale(e.Y, d.Y, height))
}

func scale(v int32, r Range, size int) int {
	if r.Max <= r.Min {
		return int(v)
	}
	return int(int64(v-r.Min) * int64(size-1) / int64(r.Max-r.Min))
}

func (d *Device) isTouchscreen() bool {
	abs := make([]byte, absY/8+1)
	keys := make([]byte, btnTouch/8+1)
	if d.ioctl(ioc(2, 'E', 0x20+evAbs, uintptr(len(abs))), unsafe.Pointer(&abs[0])) != nil ||
		d.ioctl(ioc(2, 'E', 0x20+evKey, uintptr(len(keys))), unsafe.Pointer(&keys[0])) != nil {
		return false
	}
	return hasBit(abs, absX) && hasBit(abs, absY) && hasBit(keys, btnTouch)
}

func (d *Device) absRange(axis uintptr) (Range, error) {
	var info absInfo
	if err := d.ioctl(ioc(2, 'E', 0x40+axis, unsafe.Sizeof(info)), unsafe.Pointer(&info)); err != nil {
		return Range{}, err
	}
	return Range{Min: info.Minimum, Max: info.Maximum}, nil
}

func (d *Device) ioctl(req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, d.file.Fd(), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

func hasBit(bits []byte, n int) bool {
	return bits[n/8]&(1<<(n%8)) != 0
}

// ioc builds an ioctl request number like the kernel's _IOC macro.
func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | typ<<8 | nr
}
