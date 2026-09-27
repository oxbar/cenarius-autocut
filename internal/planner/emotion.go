package planner

import (
	"math"
	"sort"
	"strings"

	"cenarius-autocut/internal/config"
)

// moodLexicon scores the emotional tone of the speech (pt-BR). Words are
// matched on whole normalized words, like the concept lexicon.
var moodLexicon = map[string][]string{
	MoodTense:     {"problema", "erro", "bug", "cuidado", "perigo", "risco", "demitido", "demissão", "faliu", "falir", "quebrou", "caiu", "vazou", "vazamento", "hacker", "golpe", "medo", "crise", "prejuízo", "urgente"},
	MoodDramatic:  {"verdade", "segredo", "ninguém", "nunca", "exposed", "polêmica", "absurdo", "surreal", "chocante", "escândalo", "mentira", "revelou", "exposto", "inacreditável"},
	MoodInspiring: {"sonho", "conquista", "conquistei", "carreira", "futuro", "oportunidade", "aprender", "crescer", "evoluir", "persistência", "mudou", "vida", "objetivo", "superar", "gratidão", "inspira"},
	MoodFunny:     {"kkk", "kkkk", "engraçado", "zoeira", "piada", "meme", "risada", "rindo", "ar", "palhaçada", "hilário", "zoando"},
	MoodChill:     {"calma", "tranquilo", "relaxa", "simples", "devagar", "rotina", "café", "tranquila"},
	MoodEnergetic: {"agora", "hoje", "rápido", "incrível", "novo", "nova", "lançou", "lançamento", "bora", "vamos", "tecnologia", "inteligência", "top", "insano", "brabo"},
}

// moodQueries are searches for licence-safe music libraries (Openverse
// indexes Jamendo, ccMixter, Freesound, Wikimedia audio).
var moodQueries = map[string][]string{
	MoodEnergetic: {"upbeat electronic", "energetic corporate", "upbeat pop instrumental"},
	MoodInspiring: {"inspiring piano", "uplifting cinematic", "motivational instrumental"},
	MoodTense:     {"suspense tension", "dark ambient tension", "thriller suspense"},
	MoodDramatic:  {"cinematic dramatic", "epic trailer", "dramatic orchestral"},
	MoodChill:     {"lofi chill", "chill hop", "calm acoustic"},
	MoodFunny:     {"funny quirky", "comedy ukulele", "playful cartoon"},
}

// IsMood reports whether m is a known mood.
func IsMood(m string) bool { _, ok := moodQueries[m]; return ok }

// MoodQueries returns the music searches for a mood.
func MoodQueries(m string) []string { return append([]string(nil), moodQueries[m]...) }

// DetectMood picks the dominant emotional tone from the semantic units.
// Punchlines weigh double (that is where emotion peaks); ties and silence
// default to energetic, which fits tech/creator content.
func DetectMood(units []SemanticUnit) (string, map[string]float64) {
	scores := map[string]float64{}
	for _, u := range units {
		w := 1.0
		if u.Role == RolePunchline || u.Emphasis {
			w = 2.0
		}
		words := strings.Fields(normalizeWord(u.Text))
		for _, raw := range words {
			word := normalizeWord(raw)
			for mood, lex := range moodLexicon {
				for _, k := range lex {
					if word == k {
						scores[mood] += w
					}
				}
			}
		}
	}
	best, bestScore := MoodEnergetic, 0.0
	// Deterministic order so equal scores always pick the same mood.
	order := []string{MoodEnergetic, MoodDramatic, MoodTense, MoodInspiring, MoodFunny, MoodChill}
	for _, m := range order {
		if scores[m] > bestScore+1e-9 {
			best, bestScore = m, scores[m]
		}
	}
	return best, scores
}

// PlanMusic builds the music cue: mood (auto or forced), queries and drops at
// the strongest punchlines (at most ~2 per minute, never in the first 3 s,
// at least 8 s apart) so the silence feels intentional, not broken.
func PlanMusic(p Plan, dur float64, cfg config.MusicConfig) *MusicCue {
	if !cfg.Enabled {
		return nil
	}
	mood, scores := DetectMood(p.SemanticUnits)
	reason := "mood detectado pela fala"
	if IsMood(cfg.Mood) {
		mood, reason = cfg.Mood, "mood forçado na configuração"
	} else if p.Music != nil && IsMood(p.Music.Mood) {
		mood, reason = p.Music.Mood, "mood escolhido pelo diretor (Ollama)"
	}
	if reason == "mood detectado pela fala" && len(scores) == 0 {
		reason = "sem palavras de emoção: energetic (padrão para tech/creator)"
	}
	cue := &MusicCue{Mood: mood, Queries: MoodQueries(mood), GainDB: cfg.GainDB, Reason: reason}
	if cfg.Drops {
		cue.Drops = planDrops(p.SemanticUnits, p.VisualEvents, dur)
	}
	return cue
}

func planDrops(units []SemanticUnit, events []VisualEvent, dur float64) []MusicDrop {
	limit := int(math.Max(1, math.Round(dur/30)))
	type cand struct {
		at     float64
		weight float64
		reason string
	}
	var cs []cand
	for i, u := range units {
		if i == 0 || u.Start < 3 || (dur > 0 && u.Start > dur-1.5) {
			continue
		}
		switch {
		case u.Role == RolePunchline:
			cs = append(cs, cand{u.Start, 1.0, "punchline"})
		case u.Role == RoleTurn && u.Emphasis:
			cs = append(cs, cand{u.Start, 0.7, "virada com ênfase"})
		}
	}
	// A fullscreen cutaway on a punchline is already a visual beat; pair it.
	for _, e := range events {
		if e.Layout == LayoutFullscreen && e.Role == RolePunchline {
			cs = append(cs, cand{e.Start, 1.1, "cutaway de punchline"})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].weight > cs[j].weight })
	var out []MusicDrop
	for _, c := range cs {
		if len(out) >= limit {
			break
		}
		ok := true
		for _, d := range out {
			if math.Abs(d.Start-c.at) < 8 {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, MusicDrop{Start: round3(c.at - 0.05), End: round3(math.Min(c.at+0.9, dur)), Reason: c.reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// IsEmotionSFX tells emotional accents (hit/riser/impact) apart from the
// insert sounds (pop/whoosh/click) that follow the "never on every image" rule.
func IsEmotionSFX(name string) bool { return name == "hit" || name == "riser" || name == "impact" }

// EmotionSFX adds a riser that builds into each drop and an impact on it,
// plus one soft hit on the hook. Gains stay inside the discreet -28..-18 dB
// band enforced by the renderer. Existing SFX within 0.4 s win (no stacking).
func EmotionSFX(p Plan, existing []SFXEvent) []SFXEvent {
	var out []SFXEvent
	busy := func(t float64, allow ...string) bool {
	next:
		for _, s := range append(append([]SFXEvent(nil), existing...), out...) {
			for _, a := range allow {
				if s.Name == a {
					continue next
				}
			}
			if math.Abs(s.Time-t) < 0.4 {
				return true
			}
		}
		return false
	}
	if p.Music != nil && (p.Music.Mood == MoodEnergetic || p.Music.Mood == MoodDramatic || p.Music.Mood == MoodTense) && !busy(0.05) {
		out = append(out, SFXEvent{Time: 0.05, Name: "hit", GainDB: -22, Reason: "impacto do gancho"})
	}
	if p.Music == nil {
		return out
	}
	for _, d := range p.Music.Drops {
		at := d.Start + 0.05
		if r := at - 0.9; r > 0.3 && !busy(r) {
			out = append(out, SFXEvent{Time: round3(r), Name: "riser", GainDB: -22, Reason: "tensão antes da " + d.Reason})
		}
		// whoosh (transition) + impact (landing) is a classic pair: allowed.
		if !busy(at, "whoosh") {
			out = append(out, SFXEvent{Time: round3(at), Name: "impact", GainDB: -18, Reason: "impacto na " + d.Reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}
