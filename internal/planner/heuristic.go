package planner

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

// Keep heuristic overlays concrete. Generic abbreviations such as "ia" are
// intentionally omitted here: the old substring matcher saw "ia" inside
// "tecnologia", "relevância" and "gostaria", creating nonsense overlays.
var techKeywords = []string{
	"inteligência artificial", "spring boot", "chatgpt", "openai", "claude", "gemini",
	"instagram", "youtube", "tiktok", "twitter", "linkedin", "github",
	"kubernetes", "docker", "java", "spring", "iphone", "apple", "google", "meta",
	"aws", "azure", "gcp",
}

var punchRE = regexp.MustCompile(`(?i)(mas|só que|detalhe|problema|produção|bug|quebrou|erro|ninguém|nunca|sexta-feira|sexta feira|agora|resultado|verdade)`)

func Heuristic(tr transcribe.Transcript, cfg config.Config) Plan {
	p := Plan{}
	if len(tr.Segments) == 0 {
		return p
	}

	// One subtle hook push-in is enough to establish motion without making the
	// camera pulse mechanically every 3 seconds.
	if cfg.Zoom.Enabled {
		s := tr.Segments[0]
		end := min(s.End, s.Start+cfg.Zoom.Duration)
		if end > s.Start+0.25 {
			p.Zooms = append(p.Zooms, ZoomEvent{Start: s.Start, End: end, Scale: cfg.Zoom.Mild, Reason: "hook"})
		}
	}

	lastZoom := -999.0
	if len(p.Zooms) > 0 {
		lastZoom = p.Zooms[len(p.Zooms)-1].Start
	}
	lastOverlay := -999.0
	usedOverlay := map[string]bool{}

	for si, s := range tr.Segments {
		text := strings.ToLower(strings.TrimSpace(s.Text))
		if text == "" {
			continue
		}

		// B-roll only for a concrete, standalone topic and never repeat the same
		// topic in the same short. This is deliberately conservative.
		if cfg.Broll.Enabled && len(p.Overlays) < cfg.Broll.MaxEvents && s.Start-lastOverlay >= 3.5 {
			if kw := concreteKeyword(text); kw != "" && !usedOverlay[kw] {
				start := s.Start + 0.15
				if start >= s.End {
					start = s.Start
				}
				end := min(s.End, start+2.2)
				if end > start+0.45 {
					p.Overlays = append(p.Overlays, OverlayEvent{Start: start, End: end, Keyword: kw, Position: "bottom", Reason: "referência visual concreta"})
					usedOverlay[kw] = true
					lastOverlay = start
				}
			}
		}

		// Punch-ins happen on actual emphasis or a new idea boundary, not on a
		// metronome. Avoid colliding with a B-roll event.
		if cfg.Zoom.Enabled && si > 0 && s.Start-lastZoom >= cfg.Zoom.MinGap && !nearOverlay(s.Start, p.Overlays) {
			scale := 0.0
			reason := ""
			if punchRE.MatchString(s.Text) || strings.ContainsAny(s.Text, "!?") {
				scale = cfg.Zoom.Punch
				reason = "ênfase/punchline"
			} else if s.Start >= 7.0 && len([]rune(strings.TrimSpace(s.Text))) >= 28 {
				scale = cfg.Zoom.Mild
				reason = "mudança de ideia"
			}
			if scale > 0 {
				end := min(s.End, s.Start+cfg.Zoom.Duration)
				if end > s.Start+0.25 {
					p.Zooms = append(p.Zooms, ZoomEvent{Start: s.Start, End: end, Scale: scale, Reason: reason})
					lastZoom = s.Start
				}
			}
		}

		if punchRE.MatchString(s.Text) {
			if w := emphasisWord(s.Text); w != "" {
				p.Emphasis = appendUnique(p.Emphasis, w)
			}
		}
	}
	sort.Slice(p.Zooms, func(i, j int) bool { return p.Zooms[i].Start < p.Zooms[j].Start })
	return p
}

func concreteKeyword(text string) string {
	for _, kw := range techKeywords {
		if matchKeyword(text, kw) {
			return kw
		}
	}
	return ""
}

func matchKeyword(text, kw string) bool {
	text = strings.ToLower(text)
	kw = strings.ToLower(strings.TrimSpace(kw))
	if kw == "" {
		return false
	}
	if strings.Contains(kw, " ") {
		return strings.Contains(text, kw)
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	})
	for _, w := range words {
		if w == kw {
			return true
		}
	}
	return false
}

func nearOverlay(t float64, os []OverlayEvent) bool {
	for _, o := range os {
		if t >= o.Start-0.6 && t <= o.End+0.6 {
			return true
		}
	}
	return false
}

func emphasisWord(s string) string {
	for _, w := range strings.Fields(s) {
		clean := strings.Trim(w, ".,!?;:\"'()[]{}")
		if len([]rune(clean)) >= 5 && !punchRE.MatchString(clean) {
			return clean
		}
	}
	return ""
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if strings.EqualFold(x, v) {
			return xs
		}
	}
	return append(xs, v)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
