package render

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cenarius-autocut/internal/planner"
)

func TestDropExprAndMusicChain(t *testing.T) {
	if DropExpr(nil) != "1" {
		t.Fatal("no drops must be neutral")
	}
	e := DropExpr([]planner.MusicDrop{{Start: 2, End: 2.9}, {Start: 12, End: 12.8}})
	for _, want := range []string{"between(t,2.000,2.900),0.05", "between(t,12.000,12.800),0.05", "(t-2.900)/0.4"} {
		if !strings.Contains(e, want) {
			t.Fatalf("missing %q in %s", want, e)
		}
	}
	if strings.Contains(e, `\,`) {
		t.Fatalf("expression is single-quoted; commas must not be escaped: %s", e)
	}
	cue := &planner.MusicCue{Mood: "tense", GainDB: -40, Drops: []planner.MusicDrop{{Start: 2, End: 2.9}}}
	c := MusicChain(3, cue, "[voicekeymusic]", 20)
	for _, want := range []string{"[3:a]", "volume=-30.0dB", "afade=t=in", "afade=t=out:st=18.200", "sidechaincompress", "[voicekeymusic]", "[music]"} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q in %s", want, c)
		}
	}
	if c2 := MusicChain(3, &planner.MusicCue{GainDB: -20}, "", 20); strings.Contains(c2, "sidechaincompress") || !strings.HasSuffix(c2, "[music]") {
		t.Fatalf("no-duck chain wrong: %s", c2)
	}
	if ClampMusicGain(-5) != -14 || ClampMusicGain(0) != -20 {
		t.Fatal("music gain must stay under the voice")
	}
}

func meanVolume(t *testing.T, path string, start, dur float64) float64 {
	t.Helper()
	out, _ := exec.Command(ffBin, "-hide_banner", "-ss", strconv.FormatFloat(start, 'f', 2, 64), "-t", strconv.FormatFloat(dur, 'f', 2, 64),
		"-i", path, "-vn", "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	m := regexp.MustCompile(`mean_volume: (-?[\d.]+) dB`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("volumedetect failed: %s", out)
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// Integration: short track looped under a silent "voice", drop at 4.0-4.9 s,
// emotional SFX; the timeline must keep its duration, have one AAC stream,
// and the music must really disappear during the drop.
func TestFinalWithMusicBedDropsAndEmotionSFX(t *testing.T) {
	cfg := need(t)
	if testing.Short() {
		t.Skip("integração")
	}
	d := t.TempDir()
	in := filepath.Join(d, "in.mp4")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=540x960:rate=30:duration=8",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", in)
	track := filepath.Join(d, "track.wav") // 3 s: must loop to cover 8 s
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration=3", track)
	plan := planner.Plan{
		Music: &planner.MusicCue{Mood: planner.MoodTense, GainDB: -16, Drops: []planner.MusicDrop{{Start: 4.0, End: 4.9, Reason: "punchline"}},
			Asset: &planner.AssetRef{Path: track, Type: "audio", Source: "music-local"}},
		// The impact normally lands inside the drop; it is left out here so the
		// level check measures the music bed alone (impact is covered by the
		// filter-graph assertion through aevalsrc of hit/riser).
		SFX: []planner.SFXEvent{{Time: 0.05, Name: "hit", GainDB: -22}, {Time: 2.9, Name: "riser", GainDB: -22}},
	}
	cfg.Captions.Enabled = false
	out := filepath.Join(d, "final.mp4")
	if err := Final(context.Background(), cfg, in, "", out, plan, true); err != nil {
		t.Fatal(err)
	}
	graph, _ := os.ReadFile(out + ".filter")
	for _, want := range []string{"sidechaincompress", "[voicekeymusic]", "aevalsrc", "between(t,4.000,4.900)"} {
		if !bytes.Contains(graph, []byte(want)) {
			t.Fatalf("filter graph missing %q", want)
		}
	}
	pb, _ := exec.Command(fpBin, "-v", "error", "-show_entries", "stream=codec_type,codec_name:format=duration", "-of", "csv=p=0", out).Output()
	s := string(pb)
	if strings.Count(s, "audio") != 1 || !strings.Contains(s, "aac") {
		t.Fatalf("expected exactly one AAC stream: %s", s)
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	dur, _ := strconv.ParseFloat(lines[len(lines)-1], 64)
	if dur < 7.8 || dur > 8.2 {
		t.Fatalf("music changed duration: %.3f", dur)
	}
	// Looped bed audible at 6.5 s (after the 3 s file ended), gone in the drop.
	before, drop, looped := meanVolume(t, out, 2.0, 0.6), meanVolume(t, out, 4.3, 0.4), meanVolume(t, out, 6.0, 0.6)
	if before-drop < 10 {
		t.Fatalf("drop not audible: before %.1f dB, drop %.1f dB", before, drop)
	}
	if looped < before-6 {
		t.Fatalf("track did not loop: before %.1f dB, at 6s %.1f dB", before, looped)
	}
}

func TestEverySFXSourceRenders(t *testing.T) {
	need(t)
	for _, name := range []string{"pop", "whoosh", "click", "beep", "hit", "riser", "impact"} {
		out := filepath.Join(t.TempDir(), name+".wav")
		if b, err := exec.Command(ffBin, "-v", "error", "-y", "-filter_complex", sfxSource(name)+"[a]", "-map", "[a]", out).CombinedOutput(); err != nil {
			t.Fatalf("sfx %s invalid: %v %s", name, err, b)
		}
		if v := meanVolume(t, out, 0, 2); v < -60 {
			t.Fatalf("sfx %s is silent (%.1f dB)", name, v)
		}
	}
}
