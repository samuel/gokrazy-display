// Package console switches the active Linux virtual terminal into graphics
// mode so the kernel's framebuffer console does not draw over the screen.
package console

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	kdSetMode  = 0x4b3a // KDSETMODE
	kdText     = 0x00   // KD_TEXT
	kdGraphics = 0x01   // KD_GRAPHICS
)

// Graphics is a console in graphics mode.
type Graphics struct {
	file *os.File
}

// EnterGraphics puts the active console (/dev/tty0) into graphics mode.
func EnterGraphics() (*Graphics, error) {
	f, err := os.OpenFile("/dev/tty0", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlSetInt(int(f.Fd()), kdSetMode, kdGraphics); err != nil {
		f.Close() //nolint:errcheck // the ioctl error is more useful
		return nil, fmt.Errorf("KDSETMODE: %w", err)
	}
	return &Graphics{file: f}, nil
}

// Restore puts the console back into text mode.
func (g *Graphics) Restore() error {
	if err := unix.IoctlSetInt(int(g.file.Fd()), kdSetMode, kdText); err != nil {
		g.file.Close() //nolint:errcheck // the ioctl error is more useful
		return fmt.Errorf("KDSETMODE: %w", err)
	}
	return g.file.Close()
}
