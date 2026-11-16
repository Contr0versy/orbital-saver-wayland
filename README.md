# orbital-saver

The screensavers that shipped with my Xfce box were a floating logo and some
pop-art squares. I got bored, so I wrote my own: a wireframe solar system
where the planets actually obey Kepler.

No OpenGL, no game engine, no C. Just Go, the raw X11 protocol and some
trigonometry.

## What it does

- Icosahedra, octahedra, a dodecahedron and a ringed globe orbit a star, with
  two moons and a comet for company.
- The orbits are real ellipses. Every frame solves Kepler's equation
  `M = E − e·sin E`, so bodies speed up near the star and crawl at the far
  end. Watch the comet.
- Lines fade with depth, and the starfield has true parallax.
- Four palettes: `amber`, `cyber`, `mono`, `green`.
- One static 2.7 MB binary, about 9 MB of RAM, no flicker.

## Try it

```sh
git clone https://github.com/codetheuri/orbital-saver.git
cd orbital-saver
make preview
```

You need Go 1.19+, `make` and an X11 session.
Debian/Kali: `sudo apt install golang-go make git`.
Arch: `sudo pacman -S --needed go make git`.

The window resizes freely. `Esc` or `q` quits.

## Install it as your screensaver

```sh
make
sudo make install      # sudo make uninstall to remove
```

- **xfce4-screensaver** (tested on Kali): *Settings → Screensaver*, pick
  **Orbital Saver**. The button under the theme list opens its options.
- **xscreensaver**: add `orbital-saver --root  \n\` to the `programs:` list
  in `~/.xscreensaver`.
- **Anything else** that can hand a window to a program:
  `orbital-saver --window-id 0x3a00007`.

Listed but the preview is black? xfce4-screensaver only runs programs from a
few fixed directories, and `/usr/bin` is not one of them. `make install` puts
a symlink in `/usr/lib/xscreensaver` for exactly that reason, so check it is
there.

## Flags

| Flag | Default | |
| --- | --- | --- |
| `--preview` | | Open a resizable window |
| `--root` | | Draw on the root window, or the one in `$XSCREENSAVER_WINDOW` |
| `--window-id ID` | | Draw on an existing window (hex or decimal id) |
| `--palette NAME` | `amber` | `amber`, `cyber`, `mono`, `green` |
| `--fps N` | `60` | Target frame rate |
| `--speed X` | `1` | Time multiplier; `0` freezes, negative runs backwards |
| `--stars N` | `1500` | Background stars |
| `--line-width N` | `0` | Pixels; `0` picks 1, or 2 on 4K screens |
| `--seed N` | `0` | Layout seed; `0` is different every run |

With no target it opens the preview. Single-dash forms (`-root`) work too.

## How it works

```
cmd/orbital-saver/   flags, signals, the 60 Hz loop
internal/math3d/     vectors, rotations, camera, projection, clipping, meshes
internal/scene/      Kepler solver, planets, starfield, palettes, renderer
internal/x11/        window selection, double-buffered Pixmap, events
packaging/           Makefile, .desktop entry, xscreensaver settings XML
```

All the 3D happens on the CPU in plain `float64`: solve each orbit, spin each
mesh, move it into camera space, clip, project. What comes out is a list of
2D lines, each tagged with one of 16 brightness steps picked from its depth.

The X server only draws those lines. They go into an off-screen Pixmap, one
`PolySegment` request per shade, and a single `CopyArea` shows the finished
frame. A half-drawn frame never reaches the screen, and since no framebuffer
lives in the program, memory stays flat at any resolution.

## Hacking

```sh
make test                                # vet + unit tests, no X server needed
go test ./internal/scene -bench Frame    # CPU cost of one frame
```

- New palette: one entry in `internal/scene/palette.go`.
- New planet: `build` in `internal/scene/scene.go`.
- New shape: a generator in `internal/math3d/mesh.go`.

## License

MIT, see [LICENSE](LICENSE).
