package planner

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

type visualTopic struct {
	Aliases []string
	Keyword string
	Query   string
	Mode    string
}

// Queries are intentionally visual and concrete. They are used first against
// the local asset library and then (free, no API key) against Wikimedia Commons.
var visualTopics = []visualTopic{
	{[]string{"vídeo teste", "video teste", "testando"}, "gravação", "content creator recording smartphone video camera", "reaction"},
	{[]string{"inteligência artificial", "inteligencia artificial"}, "inteligência artificial", "artificial intelligence technology neural network video", "reaction"},
	{[]string{"chatgpt"}, "chatgpt", "ChatGPT OpenAI interface", "card"},
	{[]string{"openai"}, "openai", "OpenAI logo", "card"},
	{[]string{"claude"}, "claude", "Claude AI Anthropic", "card"},
	{[]string{"gemini"}, "gemini", "Google Gemini artificial intelligence", "card"},
	{[]string{"instagram"}, "instagram", "Instagram app interface", "card"},
	{[]string{"youtube"}, "youtube", "YouTube app interface", "card"},
	{[]string{"tiktok"}, "tiktok", "TikTok app interface", "card"},
	{[]string{"twitter", "x"}, "twitter", "X Twitter app interface", "card"},
	{[]string{"linkedin"}, "linkedin", "LinkedIn app interface", "card"},
	{[]string{"github"}, "github", "GitHub software development", "card"},
	{[]string{"java"}, "java", "Java programming source code", "reaction"},
	{[]string{"spring boot", "spring"}, "spring boot", "Spring Framework Java programming", "reaction"},
	{[]string{"docker"}, "docker", "Docker software containers terminal", "reaction"},
	{[]string{"kubernetes"}, "kubernetes", "Kubernetes cloud infrastructure", "reaction"},
	{[]string{"iphone"}, "iphone", "Apple iPhone smartphone technology", "reaction"},
	{[]string{"apple"}, "apple", "Apple technology devices", "card"},
	{[]string{"google"}, "google", "Google technology office", "card"},
	{[]string{"aws"}, "aws", "cloud computing data center servers", "reaction"},
	{[]string{"azure"}, "azure", "cloud computing data center servers", "reaction"},
	{[]string{"gcp"}, "gcp", "cloud computing data center servers", "reaction"},
	{[]string{"mundo"}, "mundo digital", "global digital network technology world", "fullscreen"},
	{[]string{"tecnologia", "tecnologias"}, "tecnologia", "software development programming computer screen", "reaction"},
	{[]string{"programação", "programacao", "software", "código", "codigo"}, "programação", "software developer coding computer screen", "reaction"},
}

var punchRE = regexp.MustCompile(`(?i)(mas|só que|so que|detalhe|problema|produção|producao|bug|quebrou|erro|ninguém|ninguem|nunca|sexta-feira|sexta feira|agora|resultado|verdade)`)

func Heuristic(tr transcribe.Transcript, cfg config.Config) Plan {
	p := Plan{}
	if len(tr.Segments) == 0 && len(tr.Tokens) == 0 {
		return p
	}

	maxT := transcriptEnd(tr)
	if cfg.Zoom.Enabled && maxT > 0.4 {
		end := min(maxT, cfg.Zoom.Duration)
		p.Zooms = append(p.Zooms, ZoomEvent{Start: 0, End: end, Scale: cfg.Zoom.Mild, Reason: "hook"})
	}

	// Whisper commonly returns a whole short as one segment. Plan visual events
	// from word timestamps instead of relying on segment boundaries.
	if cfg.Broll.Enabled {
		hits := topicHits(tr.Tokens)
		last := -999.0
		used := map[string]bool{}
		for _, h := range hits {
			if len(p.Overlays) >= cfg.Broll.MaxEvents {
				break
			}
			if used[h.Topic.Keyword] || h.Start-last < 3.2 {
				continue
			}
			start := max(0, h.Start-0.08)
			end := min(maxT, start+2.15)
			if end-start < 0.7 {
				continue
			}
			mode := h.Topic.Mode
			if mode == "" {
				mode = "bottom"
			}
			p.Overlays = append(p.Overlays, OverlayEvent{
				Start: start, End: end, Keyword: h.Topic.Keyword, Query: h.Topic.Query,
				Position: "bottom", Mode: mode, Reason: "referência visual concreta",
			})
			// A subtle pop gives the visual insertion a deliberate edit feel.
			p.SFX = append(p.SFX, SFXEvent{Time: start, Name: "pop", GainDB: -22, Reason: "entrada de elemento visual"})
			used[h.Topic.Keyword] = true
			last = start
		}
	}

	// Sentence starts are natural cut/reframe points. This works even when
	// Whisper produced only one large segment.
	if cfg.Zoom.Enabled {
		lastZoom := 0.0
		for i := 1; i < len(tr.Tokens); i++ {
			prev := tr.Tokens[i-1]
			cur := tr.Tokens[i]
			if !strings.ContainsAny(prev.Text, ".!?") {
				continue
			}
			if cur.Start-lastZoom < cfg.Zoom.MinGap || nearOverlay(cur.Start, p.Overlays) {
				continue
			}
			scale := cfg.Zoom.Mild
			reason := "mudança de ideia"
			if punchRE.MatchString(cur.Text) || strings.ContainsAny(prev.Text, "!?") {
				scale = cfg.Zoom.Punch
				reason = "ênfase/punchline"
			}
			end := min(maxT, cur.Start+cfg.Zoom.Duration)
			if end-cur.Start > 0.25 {
				p.Zooms = append(p.Zooms, ZoomEvent{Start: cur.Start, End: end, Scale: scale, Reason: reason})
				lastZoom = cur.Start
			}
		}
	}

	for _, s := range tr.Segments {
		if punchRE.MatchString(s.Text) {
			if w := emphasisWord(s.Text); w != "" {
				p.Emphasis = appendUnique(p.Emphasis, w)
			}
		}
	}

	sort.Slice(p.Zooms, func(i, j int) bool { return p.Zooms[i].Start < p.Zooms[j].Start })
	sort.Slice(p.Overlays, func(i, j int) bool { return p.Overlays[i].Start < p.Overlays[j].Start })
	sort.Slice(p.SFX, func(i, j int) bool { return p.SFX[i].Time < p.SFX[j].Time })
	return p
}

type topicHit struct {
	Start float64
	Topic visualTopic
}

func topicHits(tokens []transcribe.Token) []topicHit {
	if len(tokens) == 0 {
		return nil
	}
	words := make([]string, len(tokens))
	for i, t := range tokens {
		words[i] = normalizeWord(t.Text)
	}
	var out []topicHit
	for i := range words {
		for _, topic := range visualTopics {
			for _, alias := range topic.Aliases {
				parts := strings.Fields(normalizeWord(alias))
				if len(parts) == 0 || i+len(parts) > len(words) {
					continue
				}
				ok := true
				for j, part := range parts {
					if words[i+j] != part {
						ok = false
						break
					}
				}
				if ok {
					out = append(out, topicHit{Start: tokens[i].Start, Topic: topic})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func normalizeWord(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) })
	return strings.Join(strings.Fields(s), " ")
}

func transcriptEnd(tr transcribe.Transcript) float64 {
	maxT := 0.0
	for _, s := range tr.Segments {
		if s.End > maxT {
			maxT = s.End
		}
	}
	for _, t := range tr.Tokens {
		if t.End > maxT {
			maxT = t.End
		}
	}
	return maxT
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
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// matchKeyword is kept for backwards compatibility with tests and helpers.
// Matching is word/phrase based; it never treats "ia" as a substring of
// "tecnologia" or "gostaria".
func matchKeyword(text, kw string) bool {
	textWords := strings.Fields(normalizeWord(text))
	kwWords := strings.Fields(normalizeWord(kw))
	if len(kwWords) == 0 || len(textWords) < len(kwWords) {
		return false
	}
	for i := 0; i+len(kwWords) <= len(textWords); i++ {
		ok := true
		for j := range kwWords {
			if textWords[i+j] != kwWords[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
