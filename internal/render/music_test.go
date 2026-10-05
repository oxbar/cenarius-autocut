package render

import (
	"bytes"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cenarius-autocut/internal/config"
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
	mc := config.Default().Music
	cue := &planner.MusicCue{Mood: "tense", Drops: []planner.MusicDrop{{Start: 2, End: 2.9}}, Speech: []planner.TimeRange{{Start: 0.5, End: 1.8}}}
	c := MusicChain(3, cue, mc, 20, -7.4, "")
	for _, want := range []string{"[3:a]", "volume=-30.6dB", "afade=t=in", "afade=t=out:st=18.200", "pow(10,-10.00/20", "[music]"} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q in %s", want, c)
		}
	}
	if strings.Contains(c, "sidechaincompress") {
		t.Fatalf("speech ranges present: ducking must be the deterministic envelope: %s", c)
	}
	// Old plans without speech ranges fall back to the sidechain.
	if c2 := MusicChain(3, &planner.MusicCue{}, mc, 20, -14, "[voicekeymusic]"); !strings.Contains(c2, "sidechaincompress") {
		t.Fatalf("sidechain fallback missing: %s", c2)
	}
	mc.Duck = false
	if c3 := MusicChain(3, cue, mc, 20, -14, ""); strings.Contains(c3, "pow(10") {
		t.Fatalf("duck disabled but applied: %s", c3)
	}
}

func TestMusicGainIsRelativeToVoice(t *testing.T) {
	mc := config.Default().Music // -22 LU below a -16 LUFS voice => -38 LUFS
	if g := MusicGainDB(-7.4, mc); math.Abs(g-(-30.6)) > 0.01 {
		t.Fatalf("loud master: gain %.2f", g)
	}
	if g := MusicGainDB(-30, mc); math.Abs(g-(-8)) > 0.01 {
		t.Fatalf("quiet track: gain %.2f", g)
	}
	mc.VoiceGainDB = 3 // voice up => music follows, keeping the same distance
	if g := MusicGainDB(-14, mc); math.Abs(g-(-21)) > 0.01 {
		t.Fatalf("voice trim: gain %.2f", g)
	}
}

func TestVoiceGain(t *testing.T) {
	mc := config.Default().Music
	if g := VoiceGainDB(-32, mc); math.Abs(g-16) > 0.01 {
		t.Fatalf("quiet iPhone voice must be lifted to -16 LUFS: %.2f", g)
	}
	mc.VoiceGainDB = -2
	if g := VoiceGainDB(math.NaN(), mc); g != -2 {
		t.Fatalf("unknown loudness: only the trim: %.2f", g)
	}
}

func TestDuckExprMergesAndRamps(t *testing.T) {
	if DuckExpr(nil, 10) != "1" {
		t.Fatal("no speech: neutral")
	}
	e := DuckExpr([]planner.TimeRange{{Start: 1, End: 2}, {Start: 2.3, End: 3}, {Start: 6, End: 7}}, 12)
	if strings.Count(e, "clip(") != 2 {
		t.Fatalf("ranges closer than the ramps must merge: %s", e)
	}
	if !strings.Contains(e, "pow(10,-12.00/20*min(1,") || !strings.Contains(e, "(t-0.800)/0.20") || !strings.Contains(e, "(3.200-t)/0.20") {
		t.Fatalf("unexpected envelope: %s", e)
	}
}

func TestMeasureLUFS(t *testing.T) {
	need(t)
	loud := filepath.Join(t.TempDir(), "loud.wav")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=6", "-af", "volume=0dB", loud)
	quiet := filepath.Join(t.TempDir(), "quiet.wav")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=6", "-af", "volume=-20dB", quiet)
	a, err := MeasureLUFS(context.Background(), ffBin, loud)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MeasureLUFS(context.Background(), ffBin, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if d := a - b; d < 19 || d > 21 {
		t.Fatalf("loudness difference %.1f LU, want ~20", d)
	}
	silent := filepath.Join(t.TempDir(), "silent.wav")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "5", silent)
	if _, err := MeasureLUFS(context.Background(), ffBin, silent); err == nil {
		t.Fatal("silent track must be rejected")
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
	if bytes.Contains(graph, []byte("[0:a]loudnorm")) {
		t.Fatal("loudnorm on the voice before amix drops the last ~3 s")
	}
	for _, want := range []string{"[voicen]", "alimiter", "aevalsrc", "between(t,4.000,4.900)"} {
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

func bandVolume(t *testing.T, path string, freq int, start, dur float64) float64 {
	t.Helper()
	out, _ := exec.Command(ffBin, "-hide_banner", "-ss", strconv.FormatFloat(start, 'f', 2, 64), "-t", strconv.FormatFloat(dur, 'f', 2, 64),
		"-i", path, "-vn", "-af", "bandpass=f="+strconv.Itoa(freq)+":width_type=q:w=8,bandpass=f="+strconv.Itoa(freq)+":width_type=q:w=8,volumedetect", "-f", "null", "-").CombinedOutput()
	m := regexp.MustCompile(`mean_volume: (-?[\d.]+) dB`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("volumedetect failed: %s", out)
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// The user's rule: the music must never be louder than the voice. A quiet
// iPhone voice (~-32 LUFS, 300 Hz) against a loud mastered track (~-7 LUFS,
// 2 kHz): after the render the voice must dominate by a wide margin while
// speaking, and the music must stay clearly below it in the pauses too.
func TestVoiceAlwaysPrevailsOverMusic(t *testing.T) {
	cfg := need(t)
	if testing.Short() {
		t.Skip("integração")
	}
	d := t.TempDir()
	in := filepath.Join(d, "in.mp4")
	// speech in [0.8,3],[3.8,6],[6.8,9]; pauses in between
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=540x960:rate=30:duration=9",
		"-f", "lavfi", "-i", "aevalsrc='0.05*sin(2*PI*300*t)*gt(mod(t,3),0.8)':s=48000:d=9",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", in)
	track := filepath.Join(d, "loud.wav")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "aevalsrc='0.9*sin(2*PI*2000*t)':s=48000:d=12", track)
	plan := planner.Plan{Music: &planner.MusicCue{Mood: planner.MoodEnergetic,
		Speech: []planner.TimeRange{{Start: 0.8, End: 3}, {Start: 3.8, End: 6}, {Start: 6.8, End: 9}},
		Asset:  &planner.AssetRef{Path: track, Type: "audio", Source: "music-local"}}}
	cfg.Captions.Enabled = false
	out := filepath.Join(d, "final.mp4")
	if err := Final(context.Background(), cfg, in, "", out, plan, true); err != nil {
		t.Fatal(err)
	}
	voice := bandVolume(t, out, 300, 1.2, 1.4)
	musicSpeaking := bandVolume(t, out, 2000, 1.2, 1.4)
	musicPause := bandVolume(t, out, 2000, 3.25, 0.3)
	t.Logf("voz %.1f dB | música falando %.1f dB | música na pausa %.1f dB", voice, musicSpeaking, musicPause)
	if voice-musicSpeaking < 25 {
		t.Fatalf("music too loud while speaking: voice %.1f dB, music %.1f dB", voice, musicSpeaking)
	}
	if voice-musicPause < 15 {
		t.Fatalf("music too loud in the pause: voice %.1f dB, music %.1f dB", voice, musicPause)
	}
	if musicPause-musicSpeaking < 6 {
		t.Fatalf("ducking not audible: pause %.1f dB vs speaking %.1f dB", musicPause, musicSpeaking)
	}
	pb, _ := exec.Command(fpBin, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out).Output()
	if dur, _ := strconv.ParseFloat(strings.TrimSpace(string(pb)), 64); dur < 8.8 || dur > 9.2 {
		t.Fatalf("duration changed: %.3f", dur)
	}
}

// Regression: the balanced mix must not end before the timeline (amix
// duration=first dropped ~0.13 s in the full graph).
func TestBalancedMixKeepsFullAudioLength(t *testing.T) {
	cfg := need(t)
	if testing.Short() {
		t.Skip("integração")
	}
	d := t.TempDir()
	in := filepath.Join(d, "in.mp4")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=540x960:rate=30:duration=7",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=7", "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", in)
	track := filepath.Join(d, "t.wav")
	run(t, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=2000:sample_rate=48000:duration=4", track)
	plan := planner.Plan{
		Music: &planner.MusicCue{Speech: []planner.TimeRange{{Start: 0.5, End: 6.5}}, Asset: &planner.AssetRef{Path: track, Type: "audio"}},
		SFX:   []planner.SFXEvent{{Time: 1, Name: "pop"}, {Time: 3, Name: "impact"}},
	}
	cfg.Captions.Enabled = false
	out := filepath.Join(d, "o.mp4")
	if err := Final(context.Background(), cfg, in, "", out, plan, true); err != nil {
		t.Fatal(err)
	}
	inDur, outA := streamDur(t, in, "a"), streamDur(t, out, "a")
	if outA < inDur-0.06 {
		t.Fatalf("audio shortened: in %.3f out %.3f", inDur, outA)
	}
	if v := streamDur(t, out, "v"); v < streamDur(t, in, "v")-0.05 {
		t.Fatalf("video shortened: %.3f", v)
	}
}

func streamDur(t *testing.T, path, sel string) float64 {
	t.Helper()
	b, err := exec.Command(fpBin, "-v", "error", "-select_streams", sel+":0", "-show_entries", "stream=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	return v
}
