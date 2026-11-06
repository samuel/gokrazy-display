// Command fbtouch is a test program for a framebuffer display with a
// touchscreen and buttons. It draws color bars and a border, then draws a dot
// wherever the screen is touched. Touching the box in the top-left corner
// clears the dots. A box along the top edge for each button lights up while
// the button is pressed.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samuel/gokrazy-display/buttons"
	"github.com/samuel/gokrazy-display/console"
	"github.com/samuel/gokrazy-display/fb"
	"github.com/samuel/gokrazy-display/touch"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

var (
	fbPath    = flag.String("fb", "", "framebuffer device (overrides -fb-driver)")
	fbDriver  = flag.String("fb-driver", "ili9341", "use the first framebuffer whose driver name contains this")
	inputPath = flag.String("input", "", "touchscreen event device (default: first touchscreen found)")
	waitFor   = flag.Duration("wait", 30*time.Second, "how long to wait for the devices to appear")
	dotRadius = flag.Int("radius", 4, "radius of the dot drawn at each touch")
	gpioChip  = flag.String("gpiochip", "gpiochip0", "GPIO chip the buttons are on")
	buttonsFl = flag.String("buttons", gpioList(buttons.PiTFT28), "comma-separated button GPIOs; empty disables buttons")
)

// clearBoxSize is the size of the box in the top-left corner that clears the screen.
const clearBoxSize = 40

var (
	black  = color.RGBA{0, 0, 0, 0xff}
	white  = color.RGBA{0xff, 0xff, 0xff, 0xff}
	gray   = color.RGBA{0x60, 0x60, 0x60, 0xff}
	red    = color.RGBA{0xff, 0, 0, 0xff}
	green  = color.RGBA{0, 0xff, 0, 0xff}
	blue   = color.RGBA{0, 0, 0xff, 0xff}
	yellow = color.RGBA{0xff, 0xff, 0, 0xff}
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		slog.Error("fbtouch failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, unix.SIGTERM)
	defer stop()

	display, err := retry(ctx, openDisplay)
	if err != nil {
		return fmt.Errorf("opening framebuffer: %w", err)
	}
	defer display.Close() //nolint:errcheck // nothing to do on failure at exit
	slog.Info("framebuffer",
		"path", display.Path(),
		"driver", display.Fix.Name(),
		"width", display.Var.XRes,
		"height", display.Var.YRes,
		"bpp", display.Var.BitsPerPixel,
		"stride", display.Fix.LineLength,
		"red", display.Var.Red,
		"green", display.Var.Green,
		"blue", display.Var.Blue)

	ts, err := retry(ctx, openTouch)
	if err != nil {
		return fmt.Errorf("opening touchscreen: %w", err)
	}
	defer ts.Close() //nolint:errcheck // nothing to do on failure at exit
	slog.Info("touchscreen", "path", ts.Path(), "name", ts.Name(), "x", ts.X, "y", ts.Y)

	// The framebuffer console only draws on fb0 by default, so other
	// framebuffers don't need the console in graphics mode.
	if display.Path() == "/dev/fb0" {
		if cons, err := console.EnterGraphics(); err != nil {
			slog.Warn("could not put console into graphics mode", "err", err)
		} else {
			defer func() {
				if err := cons.Restore(); err != nil {
					slog.Warn("could not restore console", "err", err)
				}
			}()
		}
	}

	ind := &indicators{display: display, pressed: make(map[int]bool)}
	var buttonEvents <-chan buttons.Event
	if btns, err := openButtons(); err != nil {
		slog.Warn("buttons disabled", "err", err)
	} else if btns != nil {
		defer btns.Close() //nolint:errcheck // nothing to do on failure at exit
		buttonEvents = btns.Events()
		ind.gpios = btns.GPIOs()
		if ind.pressed, err = btns.Pressed(); err != nil {
			return fmt.Errorf("reading buttons: %w", err)
		}
		slog.Info("buttons", "chip", *gpioChip, "gpios", ind.gpios)
	}

	drawBackground(display)
	ind.drawAll()

	events := make(chan touch.Event)
	errs := make(chan error, 1)
	go func() {
		for {
			ev, err := ts.Read()
			if err != nil {
				errs <- err
				return
			}
			events <- ev
		}
	}()

	bounds := display.Bounds()
	clearBox := image.Rect(0, 0, clearBoxSize, clearBoxSize)
	colors := []color.RGBA{red, green, blue, yellow, white}
	var stroke int
	var wasDown bool
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return fmt.Errorf("reading touchscreen: %w", err)
		case ev := <-buttonEvents:
			slog.Info("button", "gpio", ev.GPIO, "pressed", ev.Pressed)
			ind.pressed[ev.GPIO] = ev.Pressed
			ind.draw(ev.GPIO)
		case ev := <-events:
			p := ts.Scale(ev, bounds.Dx(), bounds.Dy())
			if ev.Down != wasDown {
				slog.Info("touch", "down", ev.Down, "x", p.X, "y", p.Y, "rawX", ev.X, "rawY", ev.Y)
				if !ev.Down {
					stroke++
				}
				wasDown = ev.Down
			}
			if !ev.Down {
				continue
			}
			if p.In(clearBox) {
				drawBackground(display)
				ind.drawAll()
				continue
			}
			drawDot(display, p, *dotRadius, colors[stroke%len(colors)])
		}
	}
}

func openDisplay() (*fb.Device, error) {
	path := *fbPath
	if path == "" {
		var err error
		if path, err = fb.Find(*fbDriver); err != nil {
			return nil, fmt.Errorf("driver %q: %w", *fbDriver, err)
		}
	}
	return fb.Open(path)
}

// openButtons returns nil if no buttons are configured.
func openButtons() (*buttons.Buttons, error) {
	if *buttonsFl == "" {
		return nil, nil //nolint:nilnil // no buttons is not an error
	}
	var gpios []int
	for f := range strings.SplitSeq(*buttonsFl, ",") {
		gpio, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("invalid -buttons: %w", err)
		}
		gpios = append(gpios, gpio)
	}
	return buttons.Open(*gpioChip, gpios)
}

func gpioList(gpios []int) string {
	s := make([]string, len(gpios))
	for i, g := range gpios {
		s[i] = strconv.Itoa(g)
	}
	return strings.Join(s, ",")
}

func openTouch() (*touch.Device, error) {
	if *inputPath != "" {
		return touch.Open(*inputPath)
	}
	return touch.Find()
}

// retry calls open until it succeeds or the wait time passes. Devices may
// appear after gokrazy starts the program.
func retry[T any](ctx context.Context, open func() (T, error)) (T, error) {
	deadline := time.Now().Add(*waitFor)
	for {
		v, err := open()
		if err == nil || time.Now().After(deadline) {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// drawBackground draws color bars to check the color order, a border to check
// the visible area, and the clear box.
func drawBackground(d *fb.Device) {
	b := d.Bounds()
	d.Fill(b, black)

	bars := []color.RGBA{red, green, blue, white}
	barH := b.Dy() / 8
	barY := b.Max.Y - barH - 2
	for i, c := range bars {
		x0 := b.Min.X + b.Dx()*i/len(bars)
		x1 := b.Min.X + b.Dx()*(i+1)/len(bars)
		d.Fill(image.Rect(x0, barY, x1, barY+barH), c)
	}

	d.Fill(image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+1), white)
	d.Fill(image.Rect(b.Min.X, b.Max.Y-1, b.Max.X, b.Max.Y), white)
	d.Fill(image.Rect(b.Min.X, b.Min.Y, b.Min.X+1, b.Max.Y), white)
	d.Fill(image.Rect(b.Max.X-1, b.Min.Y, b.Max.X, b.Max.Y), white)

	d.Fill(image.Rect(0, 0, clearBoxSize, clearBoxSize), gray)
}

func drawDot(d *fb.Device, p image.Point, r int, c color.Color) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				d.Set(p.X+dx, p.Y+dy, c)
			}
		}
	}
}

// indicators draws a labeled box for each button along the top edge, filled
// while the button is pressed.
type indicators struct {
	display *fb.Device
	gpios   []int
	pressed map[int]bool
}

const (
	indicatorWidth  = 50
	indicatorHeight = 30
	indicatorGap    = 10
	indicatorTop    = 5
)

func (in *indicators) drawAll() {
	for _, gpio := range in.gpios {
		in.draw(gpio)
	}
}

func (in *indicators) draw(gpio int) {
	i := slices.Index(in.gpios, gpio)
	if i < 0 {
		return
	}
	x := clearBoxSize + indicatorGap + i*(indicatorWidth+indicatorGap)
	r := image.Rect(x, indicatorTop, x+indicatorWidth, indicatorTop+indicatorHeight)

	fg, bg := white, black
	if in.pressed[gpio] {
		fg, bg = black, green
	}
	in.display.Fill(r, white)
	in.display.Fill(r.Inset(2), bg)

	label := strconv.Itoa(gpio)
	face := basicfont.Face7x13
	d := font.Drawer{Dst: in.display, Src: image.NewUniform(fg), Face: face}
	w := d.MeasureString(label).Round()
	d.Dot = fixed.P(r.Min.X+(r.Dx()-w)/2, r.Min.Y+(r.Dy()+face.Ascent-face.Descent)/2)
	d.DrawString(label)
}
