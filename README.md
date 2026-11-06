# gokrazy-display

Framebuffer, touchscreen and button access for [gokrazy](https://gokrazy.org), plus `fbtouch`, a test program for a
display with a touchscreen and buttons. Tested with the
[Adafruit PiTFT 2.8" capacitive](https://www.adafruit.com/product/1983) (ILI9341 display over SPI, FT6206 touch
controller over I2C, four tactile switches on GPIO 17, 22, 23 and 27) on a Raspberry Pi 1 Model B.

Pure Go, no cgo. Linux only.

## Packages

- `fb`: finds a framebuffer by driver name, and opens and memory-maps `/dev/fbN` as a `draw.Image`. Supports 16, 24
  and 32 bits per pixel using the channel layout the driver reports. Opening forces a mode set, which DRM drivers need
  to enable the display when the framebuffer console isn't bound to it.
- `touch`: finds and reads an evdev touchscreen (`/dev/input/eventN` reporting `ABS_X`, `ABS_Y` and `BTN_TOUCH`) and
  scales raw coordinates to the display.
- `buttons`: reads push buttons wired between GPIO lines and ground through the GPIO character device
  (`/dev/gpiochipN`), with internal pull-ups and debouncing. `buttons.PiTFT28` lists the PiTFT 2.8" switch GPIOs in physical order,
  left to right in landscape.
- `console`: puts the active virtual terminal into graphics mode so the framebuffer console does not draw over the
  screen.

## fbtouch

Draws a white border, red/green/blue/white color bars along the bottom, and a gray box in the top-left corner. Each
touch draws a dot under the finger; touching the gray box clears the screen. A box for each button, labeled with its
GPIO, runs along the top edge and turns green while the button is pressed. Device details, touches and button presses
are logged.

Flags:

- `-fb-driver`: use the first framebuffer whose driver name (`/sys/class/graphics/fbN/name`) contains this (default
  `ili9341`, which matches `ili9341drmfb`)
- `-fb`: framebuffer device path; overrides `-fb-driver`
- `-input`: touchscreen device (default: first touchscreen found)
- `-wait`: how long to wait for the devices to appear (default 30s)
- `-radius`: dot radius in pixels
- `-buttons`: comma-separated button GPIOs (default `23,22,27,17`, left to right); empty disables buttons
- `-gpiochip`: GPIO chip the buttons are on (default `gpiochip0`)

## gokrazy setup for the PiTFT 2.8" capacitive

### Kernel

gokrazy does not load kernel modules automatically, so the drivers must be built into the kernel:

| Option                         | Purpose                                                 |
| ------------------------------ | ------------------------------------------------------- |
| `CONFIG_SPI_BCM2835`           | SPI controller                                          |
| `CONFIG_I2C_BCM2835`           | I2C controller                                          |
| `CONFIG_DRM`                   | DRM core                                                |
| `CONFIG_TINYDRM_ILI9341`       | ILI9341 panel driver                                    |
| `CONFIG_DRM_FBDEV_EMULATION`   | `/dev/fbN` on top of DRM                                |
| `CONFIG_FB_DEVICE`             | `/dev/fbN` device nodes                                 |
| `CONFIG_TOUCHSCREEN_EDT_FT5X06` | FT6206 touch controller                                |
| `CONFIG_INPUT_EVDEV`           | `/dev/input/eventN`                                     |

The firmware framebuffer (`BCM2708 FB`, HDMI/composite) stays `/dev/fb0` and keeps the console, so the panel is
`/dev/fb1` (`ili9341drmfb`).

The kernel package must also ship `overlays/pitft28-capacitive.dtbo`; the gokrazy packer copies `overlays/*.dtbo` from
the kernel package to the boot partition. [github.com/samuel/kernel-rpi-os-32](https://github.com/samuel/kernel-rpi-os-32)
(32-bit, for the Raspberry Pi 1/Zero) does both.

### Instance config

In the instance's `config.json`:

```json
"KernelPackage": "github.com/samuel/kernel-rpi-os-32/dist",
"FirmwarePackage": "github.com/gokrazy-community/firmware-rpi/dist",
"BootloaderExtraLines": [
  "dtparam=i2c_arm=on",
  "dtparam=spi=on",
  "dtoverlay=pitft28-capacitive,drm,rotate=270,touch-swapxy,touch-invx"
]
```

The `drm` overlay parameter selects the DRM `ili9341` driver instead of the staging fbtft driver. `rotate=270` gives a
320x240 landscape display (the DRM driver's rotation is 180° from fbtft's, which uses `rotate=90` for the same
orientation); `touch-swapxy` and `touch-invx` match the touch axes to that rotation. For other rotations,
adjust `touch-swapxy`, `touch-invx` and `touch-invy` until the dots in `fbtouch` land under the finger.

Add `fbtouch`:

```sh
gok -i <instance> add github.com/samuel/gokrazy-display/cmd/fbtouch
```

## Development

```sh
go test ./fb ./touch
GOOS=linux GOARCH=arm GOARM=6 golangci-lint run ./...
```
