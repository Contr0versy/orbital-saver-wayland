# orbital-saver

A minimalist, retro screensaver for Linux/X11: a 3D wireframe solar system
whose planets follow real elliptical Keplerian orbits.

- **Small.** One static ~2.7 MB binary, about 9 MB of RAM at 1080p, no
  OpenGL, no game engine, no C libraries. The only dependency is
  [`jezek/xgb`](https://github.com/jezek/xgb), a pure-Go X11 protocol binding.
- **Flicker-free.** Every frame is drawn into an off-screen X11 Pixmap and
  shown with a single `CopyArea`.
- **Real orbital mechanics.** Each body is placed by solving Kepler's
  equation, so planets (and especially the comet) speed up near the star and
  slow down far from it.
- **Retro look.** Wireframe icosahedra, octahedra, dodecahedra and a ringed
  globe on tilted axes; lines fade into the background with depth; a starfield
  with true parallax; four colour palettes.
- **Works with your screensaver daemon.** Understands the `xscreensaver`
  conventions (`--root`, `--window-id`, `$XSCREENSAVER_WINDOW`), so it plugs
  into xscreensaver, xfce4-screensaver and mate-screensaver.

## Requirements

- An X11 session (Xorg). On Wayland only `--preview` works, through XWayland.
- Go 1.19 or newer, `make` and `git` to build.

## Install

### Debian / Ubuntu / Kali

```sh
sudo apt install golang-go make git
git clone https://github.com/codetheuri/orbital-saver.git
cd orbital-saver
make
sudo make install
```

### Arch Linux

```sh
sudo pacman -S --needed go make git
git clone https://github.com/codetheuri/orbital-saver.git
cd orbital-saver
make
sudo make install
```

`make` builds `bin/orbital-saver`. `make install` copies:

| File | Destination |
| --- | --- |
| `bin/orbital-saver` | `/usr/bin/orbital-saver` |
| `packaging/orbital-saver.desktop` | `/usr/share/applications/screensavers/` |
| `packaging/orbital-saver.xml` | `/usr/share/xscreensaver/config/` |
| symlink to the binary | `/usr/lib/xscreensaver/orbital-saver` |

The symlink matters: xfce4-screensaver only starts theme programs found in a
few fixed directories (`/usr/lib/xscreensaver` is one, `/usr/bin` is not).
Without it the theme is listed but the preview stays black.

Run `make` as your normal user first: `install` never invokes the Go
toolchain, so nothing is compiled as root. `PREFIX` and `DESTDIR` are
honoured for packaging. To remove everything: `sudo make uninstall`.

## Try it

No installation is needed to look at it:

```sh
make preview                                  # build, then open a window
./bin/orbital-saver --preview --palette cyber
./bin/orbital-saver --preview --palette mono --speed 4
```

The preview window can be resized freely; the projection rescales to fit.
Press `Esc` or `q`, or close the window, to quit.

## Command-line reference

```
orbital-saver [--preview | --root | --window-id ID] [options]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--preview` | | Open a resizable desktop window. |
| `--root` | | Draw on the root window, or on the window a screensaver daemon passes in `$XSCREENSAVER_WINDOW`. |
| `--window-id ID` | | Draw on an existing window. `ID` is hex (`0x3a00007`) or decimal. |
| `--palette NAME` | `amber` | Colour theme, see below. |
| `--fps N` | `60` | Target frame rate, 1–240. |
| `--speed X` | `1` | Time multiplier. `2` is twice as fast, `0` freezes, negative runs backwards. |
| `--stars N` | `1500` | Number of background stars. |
| `--line-width N` | `0` | Line thickness in pixels. `0` picks 1 px, or 2 px on 4K-class screens. |
| `--seed N` | `0` | Seed for orbit orientation, starting positions and the starfield. `0` is different every run. |
| `--version` | | Print the version and exit. |

With no target flag, `--preview` is used, unless `$XSCREENSAVER_WINDOW` is
set, in which case it behaves like `--root`. If several target flags are
given, `--window-id` wins over `--root`, which wins over `--preview`.
Single-dash spellings (`-root`, `-window-id 0x...`) are accepted too.

### Palettes

| Name | Look |
| --- | --- |
| `amber` | Vintage amber phosphor monochrome |
| `cyber` | Muted cyan with gold accents |
| `mono` | Crisp white on black |
| `green` | P1 green phosphor terminal |

## Using it as your screensaver

### xfce4-screensaver / mate-screensaver

After `sudo make install`, open *Settings → Screensaver* and pick
**Orbital Saver** from the theme list; the preview pane shows it running.
With the theme selected, the button under the list opens its settings
(speed, frame rate, stars, palette, line width).

### xscreensaver

Add a line to the `programs:` section of `~/.xscreensaver`:

```
programs:                                           \
                orbital-saver --root                \n\
```

then run `xscreensaver-settings` (called `xscreensaver-demo` in older
releases). *Orbital Saver* appears in the list, with sliders for speed,
frame rate, stars and palette.

### Anything else

Any locker that can hand a window to an external program can use it:

```sh
orbital-saver --window-id 0x3a00007
```

## How it works

```
cmd/orbital-saver/   CLI flags, signal handling, the frame loop
internal/math3d/     vectors, rotation matrices, camera, perspective
                     projection, line clipping, mesh generators
internal/scene/      Kepler solver, bodies, starfield, palettes, renderer
internal/x11/        X connection, target-window selection, double-buffered
                     Pixmap surface, event translation
packaging/           Makefile, .desktop entry, xscreensaver settings XML
```

Each frame:

1. **Simulate.** Every body's mean anomaly advances linearly with time;
   Newton–Raphson solves Kepler's equation `M = E − e·sin E` for the
   eccentric anomaly, which gives the position on the ellipse.
2. **Transform.** Mesh vertices are rotated about the body's tilted axis,
   moved to its orbital position and transformed into camera space. All of
   this is plain `float64` arithmetic on the CPU.
3. **Clip and project.** Lines are clipped against the near plane in 3D,
   perspective-projected, then clipped against the window rectangle.
4. **Shade.** A line's depth selects one of 16 brightness steps between the
   background and its palette colour, so distant lines fade out. Edges on the
   far side of each body are dimmed further so wireframes read as solid.
5. **Draw.** `scene` emits 2D lines through a two-method `Canvas` interface.
   `x11.Surface` implements it by sorting lines into one batch per shade and
   sending each batch as a single `PolySegment` into an off-screen Pixmap,
   which is then copied to the window.

The window never holds a half-drawn frame, and no framebuffer exists on the
client side. That is why memory use stays flat regardless of resolution.

A few details worth knowing if you read the code:

- One graphics context is created per shade at start-up, so changing colour
  costs no request while drawing. A frame is about 45 X requests in total.
- After each frame the program waits for one round trip to the server. This
  paces it to what the server can display rather than flooding its queue.
  There is no vsync in the core X protocol, so tearing is up to your
  compositor.
- On `SIGINT`, `SIGTERM` or `SIGHUP`, when the target window is destroyed,
  or when the preview is closed, the Pixmap and graphics contexts are freed
  before disconnecting.

## Development

```sh
make test                                   # go vet + unit tests
go test ./internal/scene -bench Frame       # CPU cost of one frame, no X11
```

The unit tests need no X server. Useful starting points:

- **Add a palette:** one entry in the `palettes` map in
  `internal/scene/palette.go`.
- **Add or change a planet:** `build` in `internal/scene/scene.go`.
- **Add a mesh:** a generator in `internal/math3d/mesh.go` that returns
  vertices on the unit sphere and the edges between them.

## License

MIT, see [LICENSE](LICENSE).
