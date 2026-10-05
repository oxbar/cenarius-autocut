package planner

import (
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

func unitsFrom(texts ...string) []SemanticUnit {
	var toks []transcribe.Token
	t := 0.3
	for _, s := range texts {
		toks = append(toks, words(s, t, 0.35)...)
		t = toks[len(toks)-1].End + 0.6
	}
	return BuildUnits(transcribe.Transcript{Tokens: toks})
}

func TestDetectMood(t *testing.T) {
	cases := map[string][]string{
		MoodTense:     {"O servidor caiu em produção.", "O problema foi um bug grave, cuidado com o risco!"},
		MoodInspiring: {"Eu tinha um sonho de carreira.", "Hoje a conquista mostra que dá para crescer e evoluir."},
		MoodDramatic:  {"Ninguém te conta a verdade.", "O segredo é absurdo e chocante!"},
		MoodEnergetic: {"Olha isso aqui.", "Um editor de vídeo."}, // no emotion words -> default
	}
	for want, texts := range cases {
		got, _ := DetectMood(unitsFrom(texts...))
		if got != want {
			t.Fatalf("%v: got %s want %s", texts, got, want)
		}
	}
}

func TestPlanMusicDropsAndEmotionSFX(t *testing.T) {
	cfg := config.Default()
	tr := richTranscript() // contains a punchline ("Mas ninguém te conta o problema")
	p := Heuristic(tr, cfg)
	if p.Music == nil || !IsMood(p.Music.Mood) || len(p.Music.Queries) == 0 {
		t.Fatalf("music cue missing: %+v", p.Music)
	}
	if p.Music.GainDB != cfg.Music.GainDB {
		t.Fatalf("gain not propagated: %+v", p.Music)
	}
	if len(p.Music.Drops) == 0 {
		t.Fatalf("punchline drop missing: %s", DebugJSON(p))
	}
	d := p.Music.Drops[0]
	if d.Start < 3 || d.End-d.Start > 1.0 || d.End <= d.Start {
		t.Fatalf("bad drop %+v", d)
	}
	var riser, impact bool
	for _, s := range p.SFX {
		if s.Name == "riser" && s.Time < d.Start {
			riser = true
		}
		if s.Name == "impact" && s.Time >= d.Start && s.Time <= d.End {
			impact = true
		}
	}
	if !riser || !impact {
		t.Fatalf("riser/impact not aligned with drop %+v: %+v", d, p.SFX)
	}
}

func TestMusicDisabledAndForcedMood(t *testing.T) {
	cfg := config.Default()
	cfg.Music.Enabled = false
	if p := Heuristic(richTranscript(), cfg); p.Music != nil {
		t.Fatal("music must be nil when disabled")
	}
	cfg = config.Default()
	cfg.Music.Mood = MoodChill
	if p := Heuristic(richTranscript(), cfg); p.Music == nil || p.Music.Mood != MoodChill {
		t.Fatalf("forced mood ignored: %+v", p.Music)
	}
	cfg = config.Default()
	cfg.Music.Drops, cfg.Music.EmotionSFX = false, false
	p := Heuristic(richTranscript(), cfg)
	if len(p.Music.Drops) != 0 {
		t.Fatal("drops disabled but planned")
	}
	for _, s := range p.SFX {
		if IsEmotionSFX(s.Name) {
			t.Fatalf("emotion sfx disabled but planned: %+v", s)
		}
	}
}

func TestDropsAreSpacedAndLimited(t *testing.T) {
	units := []SemanticUnit{
		{ID: "u01", Start: 0, End: 2, Role: RoleHook},
		{ID: "u02", Start: 4, End: 6, Role: RolePunchline},
		{ID: "u03", Start: 7, End: 9, Role: RolePunchline}, // < 8 s from previous
		{ID: "u04", Start: 20, End: 22, Role: RolePunchline},
		{ID: "u05", Start: 29, End: 30, Role: RolePunchline},
	}
	drops := planDrops(units, nil, 30)
	if len(drops) != 1 {
		t.Fatalf("30 s video must have at most 1 drop: %+v", drops)
	}
	drops = planDrops(units, nil, 60)
	for i := 1; i < len(drops); i++ {
		if drops[i].Start-drops[i-1].Start < 8 {
			t.Fatalf("drops too close: %+v", drops)
		}
	}
}

func TestDirectorMusicMoodIsValidated(t *testing.T) {
	tr := richTranscript()
	units := BuildUnits(tr)
	p, err := ParseDirector(`{"zooms":[{"start":0,"end":0.9,"kind":"punch_zoom","scale":1.08}],"music_mood":"TENSE"}`, tr, units, config.Default())
	if err != nil || p.Music == nil || p.Music.Mood != MoodTense {
		t.Fatalf("valid mood not parsed: %+v %v", p.Music, err)
	}
	p, err = ParseDirector(`{"zooms":[{"start":0,"end":0.9,"kind":"punch_zoom","scale":1.08}],"music_mood":"heavy metal"}`, tr, units, config.Default())
	if err != nil || p.Music != nil {
		t.Fatalf("invalid mood must be ignored: %+v %v", p.Music, err)
	}
}

func TestSpeechRangesMergeBreaths(t *testing.T) {
	units := []SemanticUnit{{Start: 0.2, End: 2.0}, {Start: 2.2, End: 4.0}, {Start: 6.0, End: 7.5}}
	r := SpeechRanges(units, 0.45)
	if len(r) != 2 || r[0].Start != 0.2 || r[0].End != 4.0 || r[1].Start != 6.0 {
		t.Fatalf("%+v", r)
	}
	p := Heuristic(richTranscript(), config.Default())
	if p.Music == nil || len(p.Music.Speech) == 0 {
		t.Fatalf("music cue must carry speech ranges: %+v", p.Music)
	}
}
