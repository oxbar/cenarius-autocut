package render

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// roundedDist is the signed distance from (x,y) to a rounded rectangle of size
// w x h and corner radius r (negative inside).
func roundedDist(x, y, w, h, r float64) float64 {
	px := math.Abs(x-w/2) - (w/2 - r)
	py := math.Abs(y-h/2) - (h/2 - r)
	ox, oy := math.Max(px, 0), math.Max(py, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(px, py), 0) - r
}

// WriteRoundedMask writes a grayscale anti-aliased rounded-rectangle mask
// used with alphamerge to give cards a rounded look.
func WriteRoundedMask(path string, w, h, r int) error {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := roundedDist(float64(x)+0.5, float64(y)+0.5, float64(w), float64(h), float64(r))
			a := math.Max(0, math.Min(1, 0.5-d))
			img.SetGray(x, y, color.Gray{Y: uint8(a * 255)})
		}
	}
	return writePNG(path, img)
}

// WriteShadow writes a soft drop shadow (RGBA) for a card of size w x h. The
// returned image is (w+2*pad) x (h+2*pad).
func WriteShadow(path string, w, h, r, pad int, opacity float64) error {
	W, H := w+2*pad, h+2*pad
	img := image.NewNRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			d := roundedDist(float64(x-pad)+0.5, float64(y-pad)+0.5, float64(w), float64(h), float64(r))
			a := opacity
			if d > 0 {
				t := math.Max(0, 1-d/float64(pad))
				a = opacity * t * t
			}
			img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, uint8(a * 255)})
		}
	}
	return writePNG(path, img)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
