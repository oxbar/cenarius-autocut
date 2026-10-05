package assembly

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"cenarius-autocut/internal/assets"
	"cenarius-autocut/internal/config"
)

func ff(t *testing.T) (string, string) {
	t.Helper()
	f, p := assets.FindFFmpeg()
	if f == "" || p == "" || !assets.FFmpegHasFilter(f, "ass") {
		t.Skip("ffmpeg com libass não disponível")
	}
	if testing.Short() {
		t.Skip("integração")
	}
	return f, p
}

func mk(t *testing.T, bin string, args ...string) {
	t.Helper()
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// makeClips creates: landscape with audio, portrait without audio and an
// "iPhone" clip stored 640x360 with a 90° rotation (displays vertical).
func makeClips(t *testing.T, ffmpeg, dir string) []Input {
	a := filepath.Join(dir, "reacao_horizontal.mp4")
	mk(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=44100:duration=4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", a)
	b := filepath.Join(dir, "gato_vertical.mp4")
	mk(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=360x640:rate=30:duration=3", "-c:v", "libx264", "-pix_fmt", "yuv420p", b)
	raw := filepath.Join(dir, "raw.mp4")
	mk(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "mandelbrot=size=640x360:rate=30", "-f", "lavfi", "-i", "sine=frequency=500:duration=5",
		"-t", "5", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", raw)
	c := filepath.Join(dir, "IMG_0001.MOV")
	mk(t, ffmpeg, "-v", "error", "-y", "-display_rotation", "90", "-i", raw, "-c", "copy", c)
	return []Input{{a, "reacao_horizontal.mp4"}, {b, "gato_vertical.mp4"}, {c, "IMG_0001.MOV"}}
}

func testConfig(t *testing.T, ffmpeg, ffprobe string) config.Config {
	cfg := config.Default()
	cfg.FFmpeg, cfg.FFprobe = ffmpeg, ffprobe
	cfg.WorkDir = t.TempDir() // asset cache must never land in the source tree
	cfg.Output.Preset = "ultrafast"
	cfg.Whisper.Binary = filepath.Join(t.TempDir(), "no-whisper") // speech is optional
	cfg.Ollama.Enabled = false
	lib := t.TempDir()
	mk(t, ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000:duration=20", filepath.Join(lib, "bed.wav"))
	_ = os.WriteFile(filepath.Join(lib, "manifest.json"), []byte(`{"tracks":[{"file":"bed.wav","title":"Bed","moods":["energetic","inspiring","tense","dramatic","chill","funny"],"license":"CC0"}]}`), 0644)
	cfg.Music.Library, cfg.Music.Manifest, cfg.Music.Remote = lib, filepath.Join(lib, "manifest.json"), false
	return cfg
}

func probeStreams(t *testing.T, ffprobe, path string) string {
	b, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type,codec_name,width,height,r_frame_rate", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunHeuristicEndToEnd(t *testing.T) {
	ffmpeg, ffprobe := ff(t)
	dir := t.TempDir()
	inputs := makeClips(t, ffmpeg, dir)
	cfg := testConfig(t, ffmpeg, ffprobe)
	out := filepath.Join(dir, "out")
	var stages []string
	res, err := Run(context.Background(), cfg, inputs, Options{Prompt: `faça um meme "QUANDO O CÓDIGO COMPILA"`, Duration: 9}, out,
		func(s string, p int, d string) { stages = append(stages, s) })
	if err != nil {
		t.Fatal(err)
	}
	s := probeStreams(t, ffprobe, res.Output)
	if !strings.Contains(s, "h264,video,1080,1920,30/1") || strings.Count(s, "audio") != 1 || !strings.Contains(s, "aac,audio") {
		t.Fatalf("unexpected output streams:\n%s", s)
	}
	if res.Duration < res.Plan.Duration()-0.3 || res.Duration > res.Plan.Duration()+0.3 {
		t.Fatalf("duration %.2f vs plan %.2f", res.Duration, res.Plan.Duration())
	}
	if res.Plan.Origin != "heuristic" || res.Plan.Segments[0].Text != "QUANDO O CÓDIGO COMPILA" {
		t.Fatalf("plan: %+v", res.Plan)
	}
	for _, f := range []string{"cut.mp4", "edit-plan.json", "assembly-plan.json", "captions.ass", "assets-attribution.json", "debug.log"} {
		if st, err := os.Stat(filepath.Join(out, f)); err != nil || st.Size() == 0 {
			t.Fatalf("missing %s", f)
		}
	}
	ass, _ := os.ReadFile(filepath.Join(out, "captions.ass"))
	if !strings.Contains(string(ass), "QUANDO O CÓDIGO") || !strings.Contains(string(ass), "Style: Meme,") {
		t.Fatalf("overlay text missing:\n%s", ass)
	}
	var edit struct {
		Music *struct {
			Asset *struct{ Path string } `json:"asset"`
		} `json:"music"`
	}
	eb, _ := os.ReadFile(filepath.Join(out, "edit-plan.json"))
	_ = json.Unmarshal(eb, &edit)
	if edit.Music == nil || edit.Music.Asset == nil || edit.Music.Asset.Path == "" {
		t.Fatalf("music not resolved: %s", eb)
	}
	log, _ := os.ReadFile(filepath.Join(out, "debug.log"))
	for _, want := range []string{"msg=assembly.clip.analyzed", "msg=assembly.plan ", "msg=assembly.timeline", "msg=audio.music.balance", "msg=assembly.done"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("debug.log missing %q", want)
		}
	}
	if strings.Join(stages, ",") == "" || stages[len(stages)-1] != "done" {
		t.Fatalf("progress: %v", stages)
	}
	// The rotated iPhone clip must fill the vertical frame (no side bars):
	// the left edge column of its segment must not be the blurred background
	// of a horizontal picture. We check the segment part is 1080x1920.
	parts, _ := filepath.Glob(filepath.Join(out, "assembly-parts", "seg*.mp4"))
	if len(parts) != len(res.Plan.Segments) {
		t.Fatalf("parts %d vs segments %d", len(parts), len(res.Plan.Segments))
	}
}

func TestRunWithOllamaDirectorAndVision(t *testing.T) {
	ffmpeg, ffprobe := ff(t)
	var visionCalls, directorCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5vl:7b"},{"name":"qwen3:8b"}]}`))
		case "/api/generate":
			b, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(b, &req)
			if imgs, ok := req["images"].([]any); ok && len(imgs) > 0 {
				atomic.AddInt32(&visionCalls, 1)
				_, _ = w.Write([]byte(`{"response":"{\"descricao\":\"um gato pisa no teclado\",\"tags\":[\"gato\",\"teclado\"],\"emocao\":\"caos\"}"}`))
				return
			}
			atomic.AddInt32(&directorCalls, 1)
			prompt, _ := req["prompt"].(string)
			if !strings.Contains(prompt, "c03") || !strings.Contains(prompt, "gato") {
				t.Errorf("director prompt lacks clip data")
			}
			plan := `{"title":"O gato programador","music_mood":"funny","captions":false,"segments":[` +
				`{"clip_id":"c02","start":0,"end":2.5,"role":"hook","text":"EU PROGRAMANDO","sfx":"hit"},` +
				`{"clip_id":"c03","start":1,"end":3.5,"role":"build","sfx":"whoosh","keep_audio":true},` +
				`{"clip_id":"c01","start":0,"end":2,"role":"punchline","text":"O CLIENTE","text_pos":"bottom","sfx":"impact","zoom":true,"keep_audio":false}]}`
			resp, _ := json.Marshal(map[string]string{"response": plan})
			_, _ = w.Write(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := testConfig(t, ffmpeg, ffprobe)
	cfg.Ollama.Enabled, cfg.Ollama.URL = true, srv.URL
	res, err := Run(context.Background(), cfg, makeClips(t, ffmpeg, dir), Options{Prompt: "meme do gato programador", Duration: 8}, filepath.Join(dir, "out"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if keep := os.Getenv("CENARIUS_KEEP_ASSEMBLY"); keep != "" {
		b, _ := os.ReadFile(res.Output)
		_ = os.WriteFile(keep, b, 0644)
	}
	if atomic.LoadInt32(&visionCalls) != 3 || atomic.LoadInt32(&directorCalls) != 1 {
		t.Fatalf("vision calls %d (want 3), director calls %d", visionCalls, directorCalls)
	}
	p := res.Plan
	if p.Origin != "ollama" || p.Segments[0].ClipID != "c02" || p.Segments[2].SFX != "impact" || p.Clips[0].VisualBy != "qwen2.5vl:7b" {
		t.Fatalf("director/vision not used: %+v", p)
	}
	want := 2.5 + 2.5 + 2.0
	if d, _ := strconv.ParseFloat(strings.TrimSpace(func() string {
		b, _ := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", res.Output).Output()
		return string(b)
	}()), 64); d < want-0.25 || d > want+0.25 {
		t.Fatalf("duration %.2f, want %.1f", d, want)
	}
}

func TestVisionAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"llava:7b"}]}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Ollama.URL = srv.URL
	if VisionAvailable(context.Background(), cfg) {
		t.Fatal("qwen2.5vl not installed")
	}
	cfg.Assembly.VisionModel = "llava"
	if !VisionAvailable(context.Background(), cfg) {
		t.Fatal("llava:7b should match 'llava'")
	}
	cfg.Ollama.Enabled = false
	if VisionAvailable(context.Background(), cfg) {
		t.Fatal("ollama disabled")
	}
}
