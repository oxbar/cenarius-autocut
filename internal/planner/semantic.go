package planner

import (
	"fmt"
	"regexp"
	"strings"

	"cenarius-autocut/internal/transcribe"
)

// Roles of a semantic unit inside a short-form video.
const (
	RoleHook        = "hook"
	RoleExplanation = "explanation"
	RoleExample     = "example"
	RolePunchline   = "punchline"
	RoleTurn        = "turn" // mudança de ideia
	RoleQuestion    = "question"
	RoleCTA         = "cta"
)

// ConceptHit is a concept mentioned inside a unit, with the timestamp of the
// word that mentioned it.
type ConceptHit struct {
	Name  string  `json:"name"`
	Alias string  `json:"alias"`
	At    float64 `json:"at"`
}

// SemanticUnit is a phrase-level unit (a sentence or a breath group). The
// planner reasons over these instead of isolated keywords.
type SemanticUnit struct {
	ID       string       `json:"id"`
	Start    float64      `json:"start"`
	End      float64      `json:"end"`
	Text     string       `json:"text"`
	Role     string       `json:"role"`
	Concepts []ConceptHit `json:"concepts,omitempty"`
	Emphasis bool         `json:"emphasis,omitempty"`
}

var (
	ctaRE     = regexp.MustCompile(`(?i)\b(segue|siga|se inscreve|inscreva|comenta|comente|compartilha|compartilhe|link na bio|deixa o (like|seu like)|salva esse|ativa o sininho)\b`)
	exampleRE = regexp.MustCompile(`(?i)\b(por exemplo|tipo assim|imagina|pensa comigo|olha isso|veja isso)\b`)
	turnRE    = regexp.MustCompile(`(?i)^\s*(mas|só que|so que|porém|porem|agora|entretanto|então|entao|aí|ai)\b`)
)

// BuildUnits splits the transcript into semantic units using punctuation,
// pauses between words and a maximum phrase length. Whisper often returns a
// whole short as a single segment, so word timestamps are the primary input.
func BuildUnits(tr transcribe.Transcript) []SemanticUnit {
	type word struct {
		text       string
		start, end float64
	}
	var words []word
	for _, t := range tr.Tokens {
		if s := strings.TrimSpace(t.Text); s != "" {
			words = append(words, word{s, t.Start, t.End})
		}
	}
	if len(words) == 0 {
		// No word timestamps: approximate words linearly inside segments.
		for _, s := range tr.Segments {
			fs := strings.Fields(s.Text)
			if len(fs) == 0 || s.End <= s.Start {
				continue
			}
			step := (s.End - s.Start) / float64(len(fs))
			for i, f := range fs {
				words = append(words, word{f, s.Start + float64(i)*step, s.Start + float64(i+1)*step})
			}
		}
	}
	if len(words) == 0 {
		return nil
	}

	var units []SemanticUnit
	cur := []word{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, w := range cur {
			parts[i] = w.text
		}
		units = append(units, SemanticUnit{
			ID:    fmt.Sprintf("u%02d", len(units)+1),
			Start: cur[0].start, End: cur[len(cur)-1].end,
			Text: strings.Join(parts, " "),
		})
		cur = cur[:0]
	}
	for i, w := range words {
		cur = append(cur, w)
		endsSentence := strings.ContainsAny(lastRune(w.text), ".!?;")
		pause := 0.0
		if i+1 < len(words) {
			pause = words[i+1].start - w.end
		}
		comma := strings.HasSuffix(w.text, ",")
		switch {
		case endsSentence:
			flush()
		case pause >= 0.45:
			flush()
		case comma && len(cur) >= 9:
			flush()
		case len(cur) >= 16:
			flush()
		}
	}
	flush()

	for i := range units {
		units[i].Role = classifyRole(units, i)
		units[i].Emphasis = units[i].Role == RolePunchline || strings.Contains(units[i].Text, "!")
		units[i].Concepts = conceptsIn(units[i], tr.Tokens)
	}
	return units
}

func lastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[len(r)-1])
}

func classifyRole(units []SemanticUnit, i int) string {
	t := units[i].Text
	switch {
	case ctaRE.MatchString(t):
		return RoleCTA
	case i == 0:
		return RoleHook
	case exampleRE.MatchString(t):
		return RoleExample
	case punchRE.MatchString(t) && (strings.Contains(t, "!") || i == len(units)-1 || turnRE.MatchString(t)):
		return RolePunchline
	case strings.Contains(t, "?"):
		return RoleQuestion
	case turnRE.MatchString(t):
		return RoleTurn
	case strings.Contains(t, "!"):
		return RolePunchline
	}
	return RoleExplanation
}

// conceptsIn finds lexicon concepts inside a unit using whole-word matching
// and returns them ordered by position (one hit per concept).
func conceptsIn(u SemanticUnit, tokens []transcribe.Token) []ConceptHit {
	words := strings.Fields(normalizeWord(u.Text))
	norm := make([]string, len(words))
	for i, w := range words {
		norm[i] = normalizeWord(w)
	}
	times := unitWordTimes(u, tokens, len(norm))
	// Longest alias wins at each position and consumes its words, so
	// "banco de dados" is data, never also "banco" (bank).
	var out []ConceptHit
	seen := map[string]bool{}
	for i := 0; i < len(norm); {
		bestLen, bestC, bestAlias := 0, -1, ""
		for ci, c := range conceptLexicon {
			for _, alias := range c.Aliases {
				parts := strings.Fields(normalizeWord(alias))
				if len(parts) <= bestLen || i+len(parts) > len(norm) {
					continue
				}
				ok := true
				for j, p := range parts {
					if norm[i+j] != p {
						ok = false
						break
					}
				}
				if ok {
					bestLen, bestC, bestAlias = len(parts), ci, alias
				}
			}
		}
		if bestC < 0 {
			i++
			continue
		}
		c := conceptLexicon[bestC]
		if !seen[c.Name] {
			out = append(out, ConceptHit{Name: c.Name, Alias: bestAlias, At: times[i]})
			seen[c.Name] = true
		}
		i += bestLen
	}
	return out
}

// unitWordTimes maps the n words of a unit to their start timestamps.
func unitWordTimes(u SemanticUnit, tokens []transcribe.Token, n int) []float64 {
	out := make([]float64, n)
	var inside []float64
	for _, t := range tokens {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if t.Start >= u.Start-0.001 && t.Start <= u.End+0.001 {
			inside = append(inside, t.Start)
		}
	}
	for i := 0; i < n; i++ {
		if i < len(inside) {
			out[i] = inside[i]
		} else if n > 0 {
			out[i] = u.Start + (u.End-u.Start)*float64(i)/float64(n)
		}
	}
	return out
}
