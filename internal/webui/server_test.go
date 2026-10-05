package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cenarius-autocut/internal/app"
	"cenarius-autocut/internal/config"
)

func TestStudioStaticUI(t *testing.T) {
	h := New(config.Default()).Handler()
	for _, path := range []string{"/", "/static/app.js", "/static/styles.css"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s => %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Edição IA", "Reagir a vídeo", "Longo → Shorts", "Lucide"} {
		if !strings.Contains(body, want) {
			t.Fatalf("UI missing %q", want)
		}
	}
}

func TestMixerIsInTheUI(t *testing.T) {
	h := New(config.Default()).Handler()
	for path, wants := range map[string][]string{
		"/":                  {"Mixer de áudio", "mix-music-db", "mix-duck-db", "mix-voice-db", "mix-sfx-db", "mix-mood", "Voz em destaque"},
		"/static/app.js":     {"mixerValues", "/remix", "paintAutoDone"},
		"/static/styles.css": {".mixer", ".mix-row"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		for _, want := range wants {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestParseAudioSettings(t *testing.T) {
	form := url.Values{"music_db": {"-30"}, "duck_db": {"14"}, "voice_db": {"1.5"}, "sfx_db": {"-3"}, "music_enabled": {"true"}, "mood": {"tense"}}
	r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a, err := parseAudioSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if *a.MusicDB != -30 || *a.DuckDB != 14 || *a.VoiceDB != 1.5 || *a.SFXDB != -3 || !*a.MusicEnabled || a.Mood != "tense" {
		t.Fatalf("%+v", a)
	}
	cfg := config.Default()
	a.Apply(&cfg)
	if cfg.Music.GainDB != -30 || cfg.Music.DuckDB != 14 || cfg.Music.Mood != "tense" {
		t.Fatalf("settings not applied: %+v", cfg.Music)
	}
	// Somebody tries to push the music over the voice from the browser.
	loud := -2.0
	app.AudioSettings{MusicDB: &loud}.Apply(&cfg)
	if cfg.Music.GainDB > -config.MinVoiceHeadroomLU {
		t.Fatalf("server accepted music above the limit: %v", cfg.Music.GainDB)
	}
	for _, bad := range []url.Values{{"music_db": {"abc"}}, {"duck_db": {"NaN"}}, {"voice_db": {"999"}}} {
		r := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(bad.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := parseAudioSettings(r); err == nil {
			t.Fatalf("invalid form accepted: %v", bad)
		}
	}
	empty := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(""))
	empty.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if a, err := parseAudioSettings(empty); err != nil || a.MusicDB != nil || a.MusicEnabled != nil {
		t.Fatalf("empty form must keep defaults: %+v %v", a, err)
	}
}

func TestRemixEndpointGuards(t *testing.T) {
	cfg := config.Default()
	cfg.WorkDir = t.TempDir()
	s := New(cfg)
	h := s.Handler()
	do := func(method, path, body string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code
	}
	if c := do("POST", "/api/jobs/nope/remix", `{}`); c != 404 {
		t.Fatalf("unknown job: %d", c)
	}
	s.put(&Job{ID: "run", Mode: app.ModeAuto, Status: "running"})
	if c := do("POST", "/api/jobs/run/remix", `{"music_db":-30}`); c != 409 {
		t.Fatalf("running job must not be remixed: %d", c)
	}
	s.put(&Job{ID: "react", Mode: app.ModeReaction, Status: "done"})
	if c := do("POST", "/api/jobs/react/remix", `{}`); c != 409 {
		t.Fatalf("reaction job has no music mixer: %d", c)
	}
	if c := do("GET", "/api/jobs/run/remix", ""); c != 405 {
		t.Fatalf("GET must be rejected: %d", c)
	}
	if c := do("POST", "/api/jobs/run/remix", `not json`); c != 400 {
		t.Fatalf("bad json: %d", c)
	}
	// Two quick clicks on a finished job: the second must be refused while
	// the first remix is running (dir is empty, so the remix itself fails fast).
	doneDir := t.TempDir()
	t.Cleanup(s.Wait) // runs before doneDir is removed (cleanups are LIFO)
	s.put(&Job{ID: "done", Mode: app.ModeAuto, Status: "done", Dir: doneDir})
	first := do("POST", "/api/jobs/done/remix", `{"music_db":-30}`)
	second := do("POST", "/api/jobs/done/remix", `{"music_db":-30}`)
	if first != 202 {
		t.Fatalf("first remix: %d", first)
	}
	if second != 409 {
		t.Fatalf("second remix: %d", second)
	}
	if c := do("GET", "/api/jobs/run/video", ""); c != 409 {
		t.Fatalf("video before done: %d", c)
	}
}

func multipartBody(t *testing.T, fields map[string]string, files int) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for i := 0; i < files; i++ {
		fw, _ := mw.CreateFormFile("clips", fmt.Sprintf("clip%d.mp4", i))
		_, _ = fw.Write([]byte("not a real video"))
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func TestAssembleEndpointValidation(t *testing.T) {
	cfg := config.Default()
	cfg.WorkDir = t.TempDir()
	cfg.Assembly.MaxClips = 3
	srv := New(cfg)
	// Registered after TempDir, so it runs first: the background job must stop
	// writing into WorkDir before the temp directory is removed.
	t.Cleanup(srv.Wait)
	h := srv.Handler()
	lastBody := ""
	post := func(fields map[string]string, files int) int {
		body, ct := multipartBody(t, fields, files)
		r := httptest.NewRequest("POST", "/api/assemble", body)
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		lastBody = w.Body.String()
		return w.Code
	}
	if c := post(map[string]string{}, 2); c != 400 {
		t.Fatalf("missing prompt: %d", c)
	}
	if c := post(map[string]string{"prompt": "meme"}, 0); c != 400 {
		t.Fatalf("no clips: %d", c)
	}
	if c := post(map[string]string{"prompt": "meme"}, 4); c != 400 {
		t.Fatalf("too many clips: %d", c)
	}
	if c := post(map[string]string{"prompt": "meme", "duration": "999"}, 2); c != 400 {
		t.Fatalf("invalid duration: %d", c)
	}
	if c := post(map[string]string{"prompt": "meme", "music_db": "abc"}, 2); c != 400 {
		t.Fatalf("invalid mixer value: %d", c)
	}
	if c := post(map[string]string{"prompt": strings.Repeat("a", 1001)}, 1); c != 400 {
		t.Fatalf("prompt too long: %d", c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/assemble", nil))
	if w.Code != 405 {
		t.Fatalf("GET: %d", w.Code)
	}
	// A valid request is accepted and becomes an assembly job (the fake
	// clips then fail asynchronously in the analysis step).
	if c := post(map[string]string{"prompt": "meme do gato", "duration": "30", "captions": "off"}, 2); c != 202 {
		t.Fatalf("valid request: %d", c)
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(lastBody), &job); err != nil || job.ID == "" {
		t.Fatalf("job id missing: %s", lastBody)
	}
	srv.Wait()
	if j := srv.snapshot(job.ID); j == nil || j.Mode != ModeAssembly || j.Status != "error" || j.Error == "" {
		t.Fatalf("fake clips must end the job with a clear error after Wait: %+v", j)
	}
}

func TestAssemblyTabInUI(t *testing.T) {
	h := New(config.Default()).Handler()
	for path, wants := range map[string][]string{
		"/":                  {"Montagem IA", "assembly-files", "assembly-prompt", "assembly-duration", "mixer-slot", "assembly-run"},
		"/static/app.js":     {"/api/assemble", "paintAssemblyPlan", "renderQueue", "stageLabel"},
		"/static/styles.css": {".clip-queue", ".assembly-plan", "repeat(4,1fr)"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		for _, want := range wants {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{"../../etc/passwd": "passwd", `C:\Users\me\clip.mov`: "clip.mov", "": "clipe"} {
		if got := displayName(in); got != want {
			t.Fatalf("displayName(%q)=%q want %q", in, got, want)
		}
	}
}
