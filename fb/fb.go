// Package fb provides access to a Linux framebuffer device (/dev/fbN) as a
// draw.Image.
package fb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ioctlGetVScreenInfo = 0x4600 // FBIOGET_VSCREENINFO
	ioctlPutVScreenInfo = 0x4601 // FBIOPUT_VSCREENINFO
	ioctlGetFScreenInfo = 0x4602 // FBIOGET_FSCREENINFO

	activateNow   = 0   // FB_ACTIVATE_NOW
	activateForce = 128 // FB_ACTIVATE_FORCE
)

// Bitfield describes where a color channel is stored in a pixel.
type Bitfield struct {
	Offset   uint32
	Length   uint32
	MSBRight uint32
}

// VarScreenInfo mirrors struct fb_var_screeninfo from linux/fb.h.
type VarScreenInfo struct {
	XRes, YRes               uint32
	XResVirtual, YResVirtual uint32
	XOffset, YOffset         uint32
	BitsPerPixel             uint32
	Grayscale                uint32
	Red, Green, Blue, Transp Bitfield
	NonStd                   uint32
	Activate                 uint32
	Height, Width            uint32
	AccelFlags               uint32
	PixClock                 uint32
	LeftMargin, RightMargin  uint32
	UpperMargin, LowerMargin uint32
	HSyncLen, VSyncLen       uint32
	Sync                     uint32
	VMode                    uint32
	Rotate                   uint32
	Colorspace               uint32
	Reserved                 [4]uint32
}

// FixScreenInfo mirrors struct fb_fix_screeninfo from linux/fb.h.
type FixScreenInfo struct {
	ID                            [16]byte
	SMemStart                     uintptr
	SMemLen                       uint32
	Type, TypeAux, Visual         uint32
	XPanStep, YPanStep, YWrapStep uint16
	LineLength                    uint32
	MMIOStart                     uintptr
	MMIOLen                       uint32
	Accel                         uint32
	Capabilities                  uint16
	Reserved                      [2]uint16
}

// Name returns the driver's identification string.
func (f *FixScreenInfo) Name() string {
	name, _, _ := bytes.Cut(f.ID[:], []byte{0})
	return string(name)
}

// Device is a memory-mapped framebuffer. It implements draw.Image.
type Device struct {
	file   *os.File
	path   string
	mem    []byte
	Var    VarScreenInfo
	Fix    FixScreenInfo
	bypp   int
	stride int
	bounds image.Rectangle
}

var _ draw.Image = (*Device)(nil)

// Open opens and memory-maps a framebuffer device such as /dev/fb0, and
// enables the display output.
func Open(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	d := &Device{file: f, path: path}
	if err := d.ioctl(ioctlGetVScreenInfo, unsafe.Pointer(&d.Var)); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	// DRM fbdev emulation only enables the display (and initializes panels
	// such as the ILI9341) on a mode set. The framebuffer console does this
	// for fb0, but nothing does for other framebuffers.
	v := d.Var
	v.Activate = activateNow | activateForce
	if err := d.ioctl(ioctlPutVScreenInfo, unsafe.Pointer(&v)); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("FBIOPUT_VSCREENINFO: %w", err)
	}
	if err := d.ioctl(ioctlGetFScreenInfo, unsafe.Pointer(&d.Fix)); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("FBIOGET_FSCREENINFO: %w", err)
	}
	switch d.Var.BitsPerPixel {
	case 16, 24, 32:
	default:
		f.Close() //nolint:errcheck // the format error is more useful
		return nil, fmt.Errorf("unsupported bits per pixel: %d", d.Var.BitsPerPixel)
	}
	d.bypp = int(d.Var.BitsPerPixel) / 8
	d.stride = int(d.Fix.LineLength)
	d.bounds = image.Rect(0, 0, int(d.Var.XRes), int(d.Var.YRes))

	size := int(d.Fix.SMemLen)
	if need := d.stride * int(d.Var.YResVirtual); size < need {
		size = need
	}
	mem, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		f.Close() //nolint:errcheck // the mmap error is more useful
		return nil, fmt.Errorf("mmap: %w", err)
	}
	// Draw to the visible part of the virtual screen.
	d.mem = mem[int(d.Var.YOffset)*d.stride+int(d.Var.XOffset)*d.bypp:]
	return d, nil
}

// ErrNotFound is returned by Find when no framebuffer matches.
var ErrNotFound = errors.New("no matching framebuffer found")

// Find returns the path of the first framebuffer device whose driver name
// (/sys/class/graphics/fbN/name) contains driver.
func Find(driver string) (string, error) {
	dirs, err := filepath.Glob("/sys/class/graphics/fb*")
	if err != nil {
		return "", err
	}
	for _, dir := range dirs {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		if strings.Contains(string(name), driver) {
			return filepath.Join("/dev", filepath.Base(dir)), nil
		}
	}
	return "", ErrNotFound
}

func (d *Device) ioctl(req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, d.file.Fd(), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// Path returns the device node path.
func (d *Device) Path() string { return d.path }

// Close unmaps and closes the device.
func (d *Device) Close() error {
	err := unix.Munmap(d.mem[:cap(d.mem)])
	return errors.Join(err, d.file.Close())
}

// Bounds implements image.Image.
func (d *Device) Bounds() image.Rectangle { return d.bounds }

// ColorModel implements image.Image.
func (d *Device) ColorModel() color.Model { return color.RGBAModel }

// At implements image.Image.
func (d *Device) At(x, y int) color.Color {
	if !(image.Point{x, y}.In(d.bounds)) {
		return color.RGBA{}
	}
	v := d.load(d.offset(x, y))
	a := uint8(0xff)
	if d.Var.Transp.Length > 0 {
		a = unpack(v, d.Var.Transp)
	}
	return color.RGBA{
		R: unpack(v, d.Var.Red),
		G: unpack(v, d.Var.Green),
		B: unpack(v, d.Var.Blue),
		A: a,
	}
}

// Set implements draw.Image.
func (d *Device) Set(x, y int, c color.Color) {
	if !(image.Point{x, y}.In(d.bounds)) {
		return
	}
	d.store(d.offset(x, y), d.pixel(c))
}

// Fill sets every pixel in r to c.
func (d *Device) Fill(r image.Rectangle, c color.Color) {
	r = r.Intersect(d.bounds)
	v := d.pixel(c)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			d.store(d.offset(x, y), v)
		}
	}
}

func (d *Device) offset(x, y int) int {
	return y*d.stride + x*d.bypp
}

func (d *Device) pixel(c color.Color) uint32 {
	r, g, b, a := c.RGBA()
	return pack(r, d.Var.Red) | pack(g, d.Var.Green) | pack(b, d.Var.Blue) | pack(a, d.Var.Transp)
}

func (d *Device) store(off int, v uint32) {
	switch d.bypp {
	case 2:
		binary.LittleEndian.PutUint16(d.mem[off:], uint16(v))
	case 3:
		d.mem[off] = byte(v)
		d.mem[off+1] = byte(v >> 8)
		d.mem[off+2] = byte(v >> 16)
	case 4:
		binary.LittleEndian.PutUint32(d.mem[off:], v)
	}
}

func (d *Device) load(off int) uint32 {
	switch d.bypp {
	case 2:
		return uint32(binary.LittleEndian.Uint16(d.mem[off:]))
	case 3:
		return uint32(d.mem[off]) | uint32(d.mem[off+1])<<8 | uint32(d.mem[off+2])<<16
	default:
		return binary.LittleEndian.Uint32(d.mem[off:])
	}
}

// pack scales a 16-bit color channel into its bitfield.
func pack(v uint32, bf Bitfield) uint32 {
	if bf.Length == 0 || bf.Length > 16 {
		return 0
	}
	return (v >> (16 - bf.Length)) << bf.Offset
}

// unpack extracts a bitfield as an 8-bit color channel.
func unpack(v uint32, bf Bitfield) uint8 {
	if bf.Length == 0 || bf.Length > 16 {
		return 0
	}
	mask := uint32(1)<<bf.Length - 1
	c := (v >> bf.Offset) & mask
	return uint8(c * 0xff / mask)
}
