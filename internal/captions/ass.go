package captions

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/planner"
	"cenarius-autocut/internal/transcribe"
)

type Cue struct {
	Start, End float64
	Words      []transcribe.Token
}

// Window moves captions to a different anchor while a layout is on screen
// (e.g. the seam of a reaction split), like professional Shorts do.
type Window struct {
	Start, End float64
	Y          int // vertical centre in the 1080x1920 PlayRes
}

func GenerateASS(path string, tokens []transcribe.Token, cfg config.CaptionConfig, emphasis []string) error {
	return GenerateASSWithLayout(path, tokens, cfg, emphasis, nil)
}

// posTag returns the override that anchors a line starting at t, or "".
func posTag(t float64, ws []Window) string {
	for _, w := range ws {
		if t >= w.Start-0.04 && t < w.End {
			return fmt.Sprintf("{\\an5\\pos(540,%d)}", w.Y)
		}
	}
	return ""
}

// GenerateASSWithLayout is GenerateASS plus optional per-layout positioning.
// Style, highlight, outline, line limits and word sync are unchanged.
func GenerateASSWithLayout(path string, tokens []transcribe.Token, cfg config.CaptionConfig, emphasis []string, windows []Window) error {
	cues := groupSmartConfigured(tokens, cfg)
	em := map[string]bool{}
	for _, x := range emphasis {
		em[strings.ToLower(cleanWord(x))] = true
	}

	margin := cfg.SafeMargin
	if margin <= 0 {
		margin = 96
	}
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 1080\nPlayResY: 1920\nScaledBorderAndShadow: yes\nWrapStyle: 2\nYCbCr Matrix: TV.709\n\n")
	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\n")
	b.WriteString(fmt.Sprintf("Style: Default,%s,%d,%s,%s,%s,&H50000000,-1,-1,0,0,100,100,0,0,1,%d,%d,2,%d,%d,%d,1\n\n", cfg.FontName, cfg.FontSize, cfg.PrimaryColor, cfg.HighlightColor, cfg.OutlineColor, cfg.Outline, cfg.Shadow, margin, margin, cfg.MarginV))
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	for _, c := range cues {
		if len(c.Words) == 0 || c.End <= c.Start {
			continue
		}
		if !cfg.ActiveWord {
			start, end := shifted(c.Start, cfg.TimingOffset), shifted(c.End, cfg.TimingOffset)
			text := renderCue(c.Words, -1, cfg, em)
			text = posTag(start, windows) + "{\\fad(45,65)\\blur0.35}" + text
			b.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(start), assTime(end), text))
			continue
		}

		// Keep the complete phrase on screen and highlight exactly the word being
		// spoken. This looks much more like modern Shorts/Reels captions than
		// flashing an entirely new sentence every few words.
		for i := range c.Words {
			start := c.Words[i].Start
			if start < c.Start {
				start = c.Start
			}
			end := c.End
			if i+1 < len(c.Words) && c.Words[i+1].Start > start {
				end = c.Words[i+1].Start
			}
			if end <= start {
				end = c.Words[i].End
			}
			if end <= start {
				continue
			}
			start, end = shifted(start, cfg.TimingOffset), shifted(end, cfg.TimingOffset)
			prefix := "{\\blur0.35}"
			if i == 0 {
				prefix = "{\\fad(35,0)\\blur0.35\\fscx103\\fscy103\\t(0,100,\\fscx100\\fscy100)}"
			}
			text := posTag(start, windows) + prefix + renderCue(c.Words, i, cfg, em)
			b.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(start), assTime(end), text))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// Group is kept for backwards compatibility with the original tests/API.
func Group(tokens []transcribe.Token, maxWords int) []Cue {
	return GroupSmart(tokens, maxWords, 24, 2)
}

func GroupSmart(tokens []transcribe.Token, maxWords, maxCharsPerLine, maxLines int) []Cue {
	return groupSmart(tokens, maxWords, float64(maxCharsPerLine), maxLines)
}

func groupSmartConfigured(tokens []transcribe.Token, cfg config.CaptionConfig) []Cue {
	return groupSmart(tokens, cfg.MaxWords, maxLineUnits(cfg), cfg.MaxLines)
}

func groupSmart(tokens []transcribe.Token, maxWords int, maxLineUnits float64, maxLines int) []Cue {
	if maxWords < 1 {
		maxWords = 6
	}
	if maxLineUnits < 8 {
		maxLineUnits = 24
	}
	if maxLines < 1 {
		maxLines = 2
	}
	cp := append([]transcribe.Token(nil), tokens...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })

	out := []Cue{}
	cur := Cue{}
	flush := func() {
		if len(cur.Words) == 0 {
			return
		}
		cur.Start = cur.Words[0].Start
		cur.End = cur.Words[len(cur.Words)-1].End
		if cur.End-cur.Start < 0.42 {
			cur.End = cur.Start + 0.42
		}
		out = append(out, cur)
		cur = Cue{}
	}

	for _, t := range cp {
		if t.End <= t.Start || strings.TrimSpace(t.Text) == "" || isSpecialCaptionToken(t.Text) {
			continue
		}
		if len(cur.Words) > 0 {
			prev := cur.Words[len(cur.Words)-1]
			gap := t.Start - prev.End
			if gap > 0.50 || t.Start-cur.Words[0].Start > 2.8 {
				flush()
			}
		}

		if len(cur.Words) > 0 {
			candidate := append(append([]transcribe.Token(nil), cur.Words...), t)
			if len(candidate) > maxWords || !fitsLinesUnits(candidate, maxLineUnits, maxLines) {
				flush()
			}
		}
		cur.Words = append(cur.Words, t)
		if strings.ContainsAny(t.Text, ".!?") {
			flush()
		}
	}
	flush()
	return out
}

func renderCue(words []transcribe.Token, active int, cfg config.CaptionConfig, emphasis map[string]bool) string {
	lines := layoutLinesUnits(words, maxLineUnits(cfg), cfg.MaxLines)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := make([]string, 0, len(line))
		for _, idx := range line {
			raw := words[idx].Text
			if cfg.Uppercase {
				raw = strings.ToUpper(raw)
			}
			raw = escapeASS(raw)
			isEm := emphasis[strings.ToLower(cleanWord(words[idx].Text))]
			if idx == active {
				scale := activeScalePercent(cfg)
				raw = fmt.Sprintf("{\\c%s\\fscx%d\\fscy%d}%s{\\c%s\\fscx100\\fscy100}", cfg.HighlightColor, scale, scale, raw, cfg.PrimaryColor)
			} else if isEm {
				raw = "{\\c" + cfg.HighlightColor + "}" + raw + "{\\c" + cfg.PrimaryColor + "}"
			}
			parts = append(parts, raw)
		}
		rendered = append(rendered, strings.Join(parts, " "))
	}
	return strings.Join(rendered, "\\N")
}

func fitsLines(words []transcribe.Token, maxChars, maxLines int) bool {
	return fitsLinesUnits(words, float64(maxChars), maxLines)
}

func fitsLinesUnits(words []transcribe.Token, maxUnits float64, maxLines int) bool {
	return len(layoutLinesUnits(words, maxUnits, maxLines+1)) <= maxLines
}

func layoutLines(words []transcribe.Token, maxChars, maxLines int) [][]int {
	return layoutLinesUnits(words, float64(maxChars), maxLines)
}

// layoutLinesUnits estimates glyph width rather than treating every rune as
// equally wide. This is deliberately conservative because active-word scaling
// must still remain inside the horizontal safe area.
func layoutLinesUnits(words []transcribe.Token, maxUnits float64, maxLines int) [][]int {
	if maxUnits <= 0 {
		maxUnits = 24
	}
	var lines [][]int
	var cur []int
	curWidth := 0.0
	for i, w := range words {
		width := wordUnits(cleanWord(w.Text))
		if width == 0 {
			width = wordUnits(w.Text)
		}
		extra := width
		if len(cur) > 0 {
			extra += 0.55 // inter-word space
		}
		if len(cur) > 0 && curWidth+extra > maxUnits {
			lines = append(lines, cur)
			cur = nil
			curWidth = 0
			extra = width
		}
		cur = append(cur, i)
		curWidth += extra
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	// groupSmart normally guarantees maxLines. A single pathological word is
	// still shown rather than silently discarded.
	if maxLines > 0 && len(lines) > maxLines {
		return lines
	}
	return lines
}

func wordUnits(s string) float64 {
	var n float64
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			n += 0.55
		case strings.ContainsRune("MW@#%&QGO0", unicode.ToUpper(r)):
			n += 1.25
		case strings.ContainsRune("Iil1|!.,:'`", r):
			n += 0.55
		case unicode.IsUpper(r):
			n += 1.05
		default:
			n += 1.0
		}
	}
	return n
}

// maxLineUnits turns the actual 1080px safe width into conservative glyph
// units. MaxCharsPerLine remains a hard editorial cap.
func maxLineUnits(cfg config.CaptionConfig) float64 {
	margin := cfg.SafeMargin
	if margin <= 0 {
		margin = 112
	}
	ratio := cfg.MaxWidthRatio
	if ratio <= 0 || ratio > 0.92 {
		ratio = 0.80
	}
	usable := float64(1080 - 2*margin)
	if byRatio := 1080.0 * ratio; byRatio < usable {
		usable = byRatio
	}
	fontSize := cfg.FontSize
	if fontSize <= 0 {
		fontSize = 74
	}
	scale := cfg.ActiveScale
	if scale <= 1 || scale > 1.15 {
		scale = 1.05
	}
	// Average uppercase Latin glyph is roughly 0.58em in common sans fonts.
	units := usable / (float64(fontSize) * 0.58 * scale)
	if cap := cfg.MaxCharsPerLine; cap > 0 && units > float64(cap) {
		units = float64(cap)
	}
	if units < 8 {
		units = 8
	}
	return units
}

func activeScalePercent(cfg config.CaptionConfig) int {
	s := cfg.ActiveScale
	if s <= 1 || s > 1.15 {
		s = 1.05
	}
	return int(s*100 + 0.5)
}

func shifted(t, offset float64) float64 {
	t += offset
	if t < 0 {
		return 0
	}
	return t
}

func assTime(v float64) string {
	if v < 0 {
		v = 0
	}
	h := int(v) / 3600
	m := (int(v) % 3600) / 60
	s := int(v) % 60
	cs := int((v-float64(int(v)))*100 + 0.5)
	if cs >= 100 {
		cs = 0
		s++
	}
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
func escapeASS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "{", "\\{")
	s = strings.ReplaceAll(s, "}", "\\}")
	return s
}
func cleanWord(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}
func isSpecialCaptionToken(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "[_") || strings.HasPrefix(s, "<|")
}

func EmphasisFromPlan(p planner.Plan) []string { return p.Emphasis }
