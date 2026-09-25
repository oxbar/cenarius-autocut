package captions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

func TestGroup(t *testing.T) {
	ts := []transcribe.Token{{Text: "olá", Start: 0, End: .2}, {Text: "mundo.", Start: .21, End: .5}, {Text: "teste", Start: 1.2, End: 1.5}}
	c := Group(ts, 5)
	if len(c) != 2 {
		t.Fatalf("%v", c)
	}
}

func TestConfiguredLineWidthStaysInsideSafeArea(t *testing.T) {
	cfg := config.Default().Captions
	ts := []transcribe.Token{
		{Text: "WWW", Start: 0, End: .2}, {Text: "MUNDO", Start: .21, End: .4},
		{Text: "DIGITAL", Start: .41, End: .6}, {Text: "PROGRAMADORES", Start: .61, End: .8},
		{Text: "TRABALHANDO", Start: .81, End: 1.0}, {Text: "AGORA", Start: 1.01, End: 1.2},
	}
	cues := groupSmartConfigured(ts, cfg)
	if len(cues) == 0 {
		t.Fatal("no cues")
	}
	limit := maxLineUnits(cfg)
	for _, cue := range cues {
		lines := layoutLinesUnits(cue.Words, limit, cfg.MaxLines)
		if len(lines) > cfg.MaxLines {
			t.Fatalf("more than %d lines: %#v", cfg.MaxLines, lines)
		}
		for _, line := range lines {
			width := 0.0
			for i, idx := range line {
				if i > 0 {
					width += 0.55
				}
				width += wordUnits(cleanWord(cue.Words[idx].Text))
			}
			if width > limit+1e-9 && len(line) > 1 {
				t.Fatalf("line width %.2f exceeds safe limit %.2f", width, limit)
			}
		}
	}
}

func TestTimingOffsetAndActiveScaleAreRendered(t *testing.T) {
	cfg := config.Default().Captions
	cfg.TimingOffset = 0.06
	cfg.ActiveScale = 1.05
	path := filepath.Join(t.TempDir(), "captions.ass")
	ts := []transcribe.Token{{Text: "teste", Start: 1.0, End: 1.4}}
	if err := GenerateASS(path, ts, cfg, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "Dialogue: 0,0:00:01.06") {
		t.Fatalf("timing offset not applied:\n%s", s)
	}
	if !strings.Contains(s, `\fscx105\fscy105`) {
		t.Fatalf("active scale not applied:\n%s", s)
	}
	if strings.Contains(s, `\fscx108\fscy108`) {
		t.Fatalf("legacy 108%% active scale still present:\n%s", s)
	}
}
