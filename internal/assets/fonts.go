package assets

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	fontOnce sync.Once
	fontPath string
)

// BoldFontFile returns a heavy sans-serif font file for drawtext. Thick,
// high-contrast type is a deliberate choice for motion graphics; thin light
// fonts read badly on phones. Returns "" if none is found (drawtext default).
func BoldFontFile() string {
	fontOnce.Do(func() {
		if p := os.Getenv("CENARIUS_BOLD_FONT"); p != "" {
			if _, err := os.Stat(p); err == nil {
				fontPath = p
				return
			}
		}
		for _, p := range []string{
			"/System/Library/Fonts/Supplemental/Arial Black.ttf",
			"/System/Library/Fonts/Supplemental/Arial Bold.ttf",
			"/Library/Fonts/Arial Black.ttf",
			"/Library/Fonts/Arial Bold.ttf",
			"C:/Windows/Fonts/ariblk.ttf",
			"C:/Windows/Fonts/arialbd.ttf",
		} {
			if _, err := os.Stat(p); err == nil {
				fontPath = p
				return
			}
		}
		for _, pat := range []string{"Arial Black", "Arial:bold", "Helvetica:bold", "Liberation Sans:bold", "DejaVu Sans:bold", "sans-serif:bold"} {
			out, err := exec.Command("fc-match", "-f", "%{file}", pat).Output()
			if err == nil {
				// fc-match always answers something; only accept a heavy face.
				lp := strings.ToLower(string(out))
				if !strings.Contains(lp, "bold") && !strings.Contains(lp, "black") && !strings.Contains(lp, "heavy") && !strings.Contains(lp, "ariblk") {
					continue
				}
				if p := strings.TrimSpace(string(out)); p != "" {
					if _, err := os.Stat(p); err == nil {
						fontPath = p
						return
					}
				}
			}
		}
	})
	return fontPath
}

// DrawtextFont returns the drawtext font option (fontfile=… or font=…).
func DrawtextFont() string {
	if p := BoldFontFile(); p != "" {
		return "fontfile='" + escapePath(p) + "'"
	}
	return "font='Arial'"
}
