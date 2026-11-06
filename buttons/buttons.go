// Package buttons reads push buttons wired between GPIO lines and ground,
// using the GPIO character device (/dev/gpiochipN).
package buttons

import (
	"fmt"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

// PiTFT28 lists the GPIOs of the four tactile switches on the Adafruit PiTFT
// 2.8" boards.
var PiTFT28 = []int{17, 22, 23, 27}

const debounce = 10 * time.Millisecond

// Event is a button press or release.
type Event struct {
	GPIO    int
	Pressed bool
}

// Buttons is a set of buttons with internal pull-ups enabled.
type Buttons struct {
	lines  *gpiocdev.Lines
	events chan Event
}

// Open requests the GPIO lines on chip (e.g. "gpiochip0") as active-low inputs
// with pull-ups, so a line reads pressed when its button connects it to ground.
func Open(chip string, gpios []int) (*Buttons, error) {
	b := &Buttons{events: make(chan Event, 16)}
	lines, err := gpiocdev.RequestLines(chip, gpios,
		gpiocdev.WithConsumer("buttons"),
		gpiocdev.AsInput,
		gpiocdev.AsActiveLow,
		gpiocdev.WithPullUp,
		gpiocdev.WithBothEdges,
		gpiocdev.WithDebounce(debounce),
		gpiocdev.WithEventHandler(b.handle))
	if err != nil {
		return nil, fmt.Errorf("requesting GPIOs %v on %s: %w", gpios, chip, err)
	}
	b.lines = lines
	return b, nil
}

func (b *Buttons) handle(ev gpiocdev.LineEvent) {
	b.events <- Event{GPIO: ev.Offset, Pressed: ev.Type == gpiocdev.LineEventRisingEdge}
}

// Events returns the channel that receives button presses and releases.
func (b *Buttons) Events() <-chan Event { return b.events }

// GPIOs returns the button GPIOs in the order they were requested.
func (b *Buttons) GPIOs() []int { return b.lines.Offsets() }

// Pressed returns the current state of each button, keyed by GPIO.
func (b *Buttons) Pressed() (map[int]bool, error) {
	offsets := b.lines.Offsets()
	values := make([]int, len(offsets))
	if err := b.lines.Values(values); err != nil {
		return nil, err
	}
	pressed := make(map[int]bool, len(offsets))
	for i, gpio := range offsets {
		pressed[gpio] = values[i] != 0
	}
	return pressed, nil
}

// Close releases the GPIO lines.
func (b *Buttons) Close() error {
	return b.lines.Close()
}
