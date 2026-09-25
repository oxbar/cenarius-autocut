package render

import (
	"math"

	"cenarius-autocut/internal/config"
)

// Rect is a pixel rectangle on the 1080x1920 canvas.
type Rect struct{ X, Y, W, H int }

func (r Rect) Bottom() int { return r.Y + r.H }

// Geometry holds every layout rectangle for one output configuration. It is
// computed in one place so the renderer, the caption positioning and the tests
// agree on where things are.
type Geometry struct {
	W, H         int
	Split        int  // y of the seam for reaction/card layouts
	Top          Rect // A-roll panel in split layouts
	Bottom       Rect // B-roll panel in reaction layout
	Card         Rect // card inside the bottom panel (below seam captions)
	PIP          Rect
	CropY        int  // y offset used to reframe the creator into the top panel
	CaptionZone  Rect // default caption area (bottom captions)
	SeamCaptionY int  // centre y of captions while a split layout is on screen
	ShadowPad    int
	Radius       int
}

// ComputeGeometry derives layout rectangles from the config.
func ComputeGeometry(cfg config.Config) Geometry {
	W, H := cfg.Output.Width, cfg.Output.Height
	split := even(int(math.Round(float64(H) * cfg.Broll.ReactionSplit)))
	if split <= 0 {
		split = H / 2
	}
	focus := cfg.Broll.ReactionFocusY
	if focus <= 0 || focus >= 1 {
		focus = 0.4
	}
	cropY := int(focus*float64(H)) - split/2
	if cropY < 0 {
		cropY = 0
	}
	if cropY > H-split {
		cropY = H - split
	}
	fs := cfg.Captions.FontSize
	if fs <= 0 {
		fs = 80
	}
	lines := cfg.Captions.MaxLines
	if lines <= 0 {
		lines = 2
	}
	block := int(float64(fs*lines) * 1.18)
	capBottom := H - cfg.Captions.MarginV
	g := Geometry{
		W: W, H: H, Split: split, CropY: cropY,
		Top:          Rect{0, 0, W, split},
		Bottom:       Rect{0, split, W, H - split},
		CaptionZone:  Rect{0, capBottom - block, W, block},
		SeamCaptionY: split,
		ShadowPad:    36, Radius: 36,
	}
	// Card: centred in the bottom panel, below the half of the seam captions
	// and above the platform UI (username/description area ~10% of height).
	cardTop := split + block/2 + 36
	cardBottom := H - int(float64(H)*0.10)
	cw := even(int(float64(W) * 0.86))
	ch := even(cardBottom - cardTop)
	if ch > int(float64(cw)*0.80) {
		ch = even(int(float64(cw) * 0.80))
	}
	g.Card = Rect{(W - cw) / 2, cardTop + (cardBottom-cardTop-ch)/2, cw, ch}
	pw := even(int(float64(W) * 0.40))
	ph := even(int(float64(pw) * 0.75))
	g.PIP = Rect{W - pw - 48, even(int(float64(H) * 0.11)), pw, ph}
	return g
}

// Intersects reports whether two rectangles overlap.
func Intersects(a, b Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
