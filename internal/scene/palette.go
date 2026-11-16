package scene

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
)

// Role says what a line belongs to, and therefore which palette colour it
// is drawn in.
type Role int

const (
	RoleStar      Role = iota // the central star
	RolePlanet                // planet bodies
	RoleAccent                // rings, moons, the comet
	RoleOrbit                 // orbit outlines
	RoleStarfield             // background stars
	numRoles
)

// Levels is the number of brightness steps between the background and a
// role's full colour. Depth fading picks one of these per line. Each
// (role, level) pair in use costs one draw request per frame, so this
// trades smoothness of the fade against work for the X server.
const Levels = 16

// Palette is a colour theme: a background plus one colour per Role.
type Palette struct {
	Name        string
	Description string
	Background  color.RGBA
	Colors      [numRoles]color.RGBA
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

var palettes = map[string]Palette{
	"amber": {
		Name:        "amber",
		Description: "vintage amber phosphor monochrome",
		Background:  rgb(10, 5, 0),
		Colors: [numRoles]color.RGBA{
			RoleStar:      rgb(255, 214, 120),
			RolePlanet:    rgb(255, 176, 0),
			RoleAccent:    rgb(255, 128, 0),
			RoleOrbit:     rgb(150, 92, 0),
			RoleStarfield: rgb(170, 110, 20),
		},
	},
	"cyber": {
		Name:        "cyber",
		Description: "muted cyan with gold accents",
		Background:  rgb(4, 8, 12),
		Colors: [numRoles]color.RGBA{
			RoleStar:      rgb(235, 196, 96),
			RolePlanet:    rgb(96, 200, 204),
			RoleAccent:    rgb(214, 170, 84),
			RoleOrbit:     rgb(44, 104, 116),
			RoleStarfield: rgb(120, 150, 160),
		},
	},
	"mono": {
		Name:        "mono",
		Description: "crisp white on black",
		Background:  rgb(0, 0, 0),
		Colors: [numRoles]color.RGBA{
			RoleStar:      rgb(255, 255, 255),
			RolePlanet:    rgb(255, 255, 255),
			RoleAccent:    rgb(200, 200, 200),
			RoleOrbit:     rgb(120, 120, 120),
			RoleStarfield: rgb(170, 170, 170),
		},
	},
	"green": {
		Name:        "green",
		Description: "P1 green phosphor terminal",
		Background:  rgb(0, 8, 2),
		Colors: [numRoles]color.RGBA{
			RoleStar:      rgb(190, 255, 190),
			RolePlanet:    rgb(51, 255, 102),
			RoleAccent:    rgb(140, 255, 90),
			RoleOrbit:     rgb(20, 130, 50),
			RoleStarfield: rgb(40, 150, 70),
		},
	},
}

// PaletteNames lists the available palette names in alphabetical order.
func PaletteNames() []string {
	names := make([]string, 0, len(palettes))
	for name := range palettes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PaletteByName looks a palette up by its (case-insensitive) name.
func PaletteByName(name string) (Palette, error) {
	p, ok := palettes[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Palette{}, fmt.Errorf("unknown palette %q (available: %s)",
			name, strings.Join(PaletteNames(), ", "))
	}
	return p, nil
}

// Ramp returns the colour table a Canvas must be loaded with. Entry
// Shade(role, b) is the role's colour faded towards the background by
// brightness b. Entries are ordered dimmest first, so drawing them in index
// order lets nearer, brighter lines overdraw farther, dimmer ones.
func (p Palette) Ramp() []color.RGBA {
	ramp := make([]color.RGBA, Levels*int(numRoles))
	for level := 0; level < Levels; level++ {
		t := float64(level+1) / Levels
		for role := Role(0); role < numRoles; role++ {
			c := p.Colors[role]
			ramp[level*int(numRoles)+int(role)] = color.RGBA{
				R: mix(p.Background.R, c.R, t),
				G: mix(p.Background.G, c.G, t),
				B: mix(p.Background.B, c.B, t),
				A: 255,
			}
		}
	}
	return ramp
}

// Shade returns the Ramp index for a role at a brightness in [0, 1].
func Shade(role Role, brightness float64) int {
	level := int(brightness*(Levels-1) + 0.5)
	if level < 0 {
		level = 0
	} else if level > Levels-1 {
		level = Levels - 1
	}
	return level*int(numRoles) + int(role)
}

func mix(from, to uint8, t float64) uint8 {
	return uint8(float64(from) + (float64(to)-float64(from))*t + 0.5)
}
