package assets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/planner"
)

func makeTone(t *testing.T, path string, seconds string) {
	t.Helper()
	ff, _ := needFFmpeg(t)
	if out, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000:duration="+seconds, path).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func musicCfg(dir string) config.MusicConfig {
	c := config.Default().Music
	c.Library = dir
	c.Manifest = filepath.Join(dir, "manifest.json")
	return c
}

func musicResolver(t *testing.T) *Resolver {
	ff, fp := needFFmpeg(t)
	d := t.TempDir()
	r := New(filepath.Join(d, "assets"), filepath.Join(d, "none.json"), filepath.Join(d, "cache"))
	r.Validator = FFValidator{FFprobe: fp, FFmpeg: ff}
	r.Remote = false
	return r
}

func TestMusicLocalLibraryByMood(t *testing.T) {
	r := musicResolver(t)
	lib := t.TempDir()
	makeTone(t, filepath.Join(lib, "calm.wav"), "30")
	makeTone(t, filepath.Join(lib, "tense.wav"), "30")
	manifest := `{"tracks":[
	 {"file":"calm.wav","title":"Calm","moods":["chill"],"license":"YouTube Audio Library","author":"Artist A"},
	 {"file":"tense.wav","title":"Tension","moods":["tense","dramatic"],"license":"CC BY 4.0","author":"Artist B"}]}`
	if err := os.WriteFile(filepath.Join(lib, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	ref, a, err := r.ResolveMusic(context.Background(), &planner.MusicCue{Mood: planner.MoodDramatic}, musicCfg(lib))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Type != "audio" || !strings.HasSuffix(a.Path, "tense.wav") || a.Source != SourceMusicLocal || a.License != "CC BY 4.0" || a.Duration < 29 {
		t.Fatalf("%+v %+v", ref, a)
	}
	if _, _, err := r.ResolveMusic(context.Background(), &planner.MusicCue{Mood: planner.MoodFunny}, musicCfg(lib)); err == nil {
		t.Fatal("no funny track and remote off: must report no music (not pick a wrong mood)")
	}
}

func TestMusicForcedFileWins(t *testing.T) {
	r := musicResolver(t)
	f := filepath.Join(t.TempDir(), "mine.wav")
	makeTone(t, f, "25")
	cfg := musicCfg(t.TempDir())
	cfg.File = f
	_, a, err := r.ResolveMusic(context.Background(), &planner.MusicCue{Mood: planner.MoodChill}, cfg)
	if err != nil || a.Path != f || a.Source != SourceMusicFile {
		t.Fatalf("%+v %v", a, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.mp3")
	_ = os.WriteFile(bad, []byte("<html>not audio</html>"+strings.Repeat(" ", 200)), 0644)
	cfg.File = bad
	if _, _, err := r.ResolveMusic(context.Background(), &planner.MusicCue{Mood: planner.MoodChill}, cfg); err == nil {
		t.Fatal("invalid forced file must error")
	}
}

func TestAudioValidation(t *testing.T) {
	ff, fp := needFFmpeg(t)
	v := FFValidator{FFprobe: fp, FFmpeg: ff}
	d := t.TempDir()
	ok := filepath.Join(d, "ok.wav")
	makeTone(t, ok, "8")
	if m, err := v.Validate(context.Background(), ok, "audio"); err != nil || m.Type != "audio" || m.Duration < 7.9 {
		t.Fatalf("%+v %v", m, err)
	}
	short := filepath.Join(d, "short.wav")
	makeTone(t, short, "2")
	if _, err := v.Validate(context.Background(), short, "audio"); err == nil {
		t.Fatal("2 s track is not a music bed")
	}
	vid := filepath.Join(d, "silent.mp4")
	makeVideo(t, vid, "6") // video without audio stream
	if _, err := v.Validate(context.Background(), vid, "audio"); err == nil {
		t.Fatal("file without audio stream must be rejected")
	}
}

func TestOpenverseMusicFiltersAndAttribution(t *testing.T) {
	r := musicResolver(t)
	good := filepath.Join(t.TempDir(), "good.wav")
	makeTone(t, good, "60")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/audio/":
			q := req.URL.Query()
			if q.Get("license_type") != "commercial,modification" || q.Get("category") != "music" {
				t.Errorf("licence/category filter missing: %s", req.URL.RawQuery)
			}
			res := []map[string]any{
				{"title": "Upbeat electronic NC", "url": srv.URL + "/good.wav", "license": "by-nc", "license_version": "4.0", "duration": 90000, "genres": []string{"electronic"}},
				{"title": "Upbeat electronic jingle", "url": srv.URL + "/good.wav", "license": "by", "license_version": "4.0", "duration": 8000, "genres": []string{"electronic"}},
				{"title": "Sad violin", "url": srv.URL + "/good.wav", "license": "cc0", "duration": 90000, "genres": []string{"classical"}},
				{"title": "Neon Run", "url": srv.URL + "/good.wav", "creator": "DJ Free", "license": "by", "license_version": "4.0", "duration": 95000,
					"genres": []string{"electronic", "dance"}, "provider": "jamendo", "foreign_landing_url": "https://www.jamendo.com/track/1"},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
		case "/good.wav":
			w.Header().Set("Content-Type", "audio/wav")
			http.ServeFile(w, req, good)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()
	r.Remote, r.Client, r.OpenverseAPI = true, srv.Client(), srv.URL+"/v1"
	cue := &planner.MusicCue{Mood: planner.MoodEnergetic, Queries: planner.MoodQueries(planner.MoodEnergetic)}
	_, a, err := r.ResolveMusic(context.Background(), cue, musicCfg(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if a.Keyword != "Neon Run" || a.License != "CC BY 4.0" || a.Author != "DJ Free" || !strings.Contains(a.Attribution, "via Openverse") || a.SourceURL == "" {
		t.Fatalf("wrong track or attribution: %+v", a)
	}
	// Second resolve comes from cache, no new download needed.
	r.Remote = false
	_, a2, err := r.ResolveMusic(context.Background(), cue, musicCfg(t.TempDir()))
	if err != nil || a2.Path != a.Path || a2.Source != SourceCache {
		t.Fatalf("music cache miss: %+v %v", a2, err)
	}
}
