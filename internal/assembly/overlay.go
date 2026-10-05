package assembly

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"
)

// Overlay y positions on the 1080x1920 canvas. Top/center stay clear of the
// speech captions (bottom, MarginV 560) and bottom sits above the platform UI.
var overlayY = map[string]int{"top": 300, "center": 760, "bottom": 1560}

const memeStyle = "Style: Meme,Arial,92,&H00FFFFFF,&H00FFFFFF,&H00000000,&H64000000,-1,0,0,0,100,100,0,0,1,8,2,5,60,60,0,1\n"

// wrapOverlay upper-cases and breaks a text into at most 3 lines of ~16
// characters, returning the ASS text and a font size that keeps the longest
// line inside the safe width.
func wrapOverlay(text string) (string, int) {
	words := strings.Fields(strings.ToUpper(text))
	var lines []string
	cur := ""
	for _, w := range words {
		if cur != "" && utf8.RuneCountInString(cur+" "+w) > 16 && len(lines) < 2 {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	longest := 0
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > longest {
			longest = n
		}
	}
	size := 92
	// ~0.64 em per heavy uppercase glyph; 900 px usable width.
	if longest > 0 {
		size = int(math.Min(92, math.Max(52, 900/(float64(longest)*0.64))))
	}
	for i, l := range lines {
		lines[i] = escapeASSText(l)
	}
	return strings.Join(lines, `\N`), size
}

func escapeASSText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "{", "(", "}", ")").Replace(s)
}

func assTime(v float64) string {
	if v < 0 {
		v = 0
	}
	cs := int(math.Round(v * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, (cs/6000)%60, (cs/100)%60, cs%100)
}

// OverlayDialogues builds the meme/title lines for the placed segments.
func OverlayDialogues(lay []placed) []string {
	var out []string
	for _, s := range lay {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		text, size := wrapOverlay(s.Text)
		y := overlayY[s.TextPos]
		if y == 0 {
			y = overlayY["top"]
		}
		end := s.At + s.Duration()
		// Pop-in (85% -> 100%) and a short fade: readable, not flashy.
		out = append(out, fmt.Sprintf(`Dialogue: 5,%s,%s,Meme,,0,0,0,,{\an5\pos(540,%d)\fs%d\fad(120,150)\fscx85\fscy85\t(0,140,\fscx100\fscy100)}%s`,
			assTime(s.At+0.02), assTime(end-0.02), y, size, text))
	}
	return out
}

// WriteOverlayASS merges the meme overlays into the captions file at path.
// If the file does not exist (no speech captions) a minimal script is made.
func WriteOverlayASS(path string, lay []placed) error {
	lines := OverlayDialogues(lay)
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		if len(lines) == 0 {
			return nil
		}
		var s strings.Builder
		s.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 1080\nPlayResY: 1920\nScaledBorderAndShadow: yes\nWrapStyle: 2\n\n")
		s.WriteString("[V4+ Styles]\nFormat: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\n")
		s.WriteString(memeStyle + "\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
		for _, l := range lines {
			s.WriteString(l + "\n")
		}
		return os.WriteFile(path, []byte(s.String()), 0644)
	}
	src := string(b)
	if !strings.Contains(src, "Style: Meme,") {
		i := strings.Index(src, "\n[Events]")
		if i < 0 {
			return fmt.Errorf("legenda ASS sem seção [Events]")
		}
		// Insert the style at the end of the styles block (before the blank
		// line that precedes [Events]).
		head := strings.TrimRight(src[:i], "\n")
		src = head + "\n" + memeStyle + "\n" + src[i+1:]
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	for _, l := range lines {
		src += l + "\n"
	}
	return os.WriteFile(path, []byte(src), 0644)
}
