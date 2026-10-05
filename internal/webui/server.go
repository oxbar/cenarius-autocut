package webui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"cenarius-autocut/internal/app"
	"cenarius-autocut/internal/assembly"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/shorts"
)

//go:embed static/*
var staticFS embed.FS

type ClipOutput struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Output    string           `json:"-"`
	Candidate shorts.Candidate `json:"candidate"`
	Download  string           `json:"download"`
}

type Job struct {
	ID         string             `json:"id"`
	Mode       string             `json:"mode"`
	Status     string             `json:"status"`
	Stage      string             `json:"stage"`
	Detail     string             `json:"detail"`
	Percent    int                `json:"percent"`
	Error      string             `json:"error,omitempty"`
	Output     string             `json:"-"`
	Download   string             `json:"download,omitempty"`
	Result     *app.Result        `json:"result,omitempty"`
	Candidates []shorts.Candidate `json:"candidates,omitempty"`
	Clips      []ClipOutput       `json:"clips,omitempty"`
	Source     string             `json:"-"`
	Dir        string             `json:"-"`
	// Audio is the effective mixer of the job (after safe clamping). The UI
	// shows it and can re-render with new values through /remix.
	Audio    *config.MusicConfig `json:"audio,omitempty"`
	Assembly *assembly.Plan      `json:"assembly,omitempty"` // editor's cut (Montagem IA)
	Video    string              `json:"video,omitempty"`    // inline preview URL
	saved    app.AudioSettings
}

type Server struct {
	cfg  config.Config
	mu   sync.RWMutex
	jobs map[string]*Job
	bg   sync.WaitGroup // background jobs (see background/Wait)
}

// background runs a job goroutine tracked by the server, so tests (and a
// graceful shutdown) can wait for every job to stop writing to disk.
func (s *Server) background(fn func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		fn()
	}()
}

// Wait blocks until all background jobs have finished.
func (s *Server) Wait() { s.bg.Wait() }

func New(cfg config.Config) *Server { return &Server{cfg: cfg, jobs: map[string]*Job{}} }

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.index)
	m.Handle("/static/", http.FileServer(http.FS(staticFS)))
	m.HandleFunc("/api/jobs", s.jobsHandler)
	m.HandleFunc("/api/jobs/", s.jobHandler)
	m.HandleFunc("/api/reactions", s.reactionHandler)
	m.HandleFunc("/api/assemble", s.assembleHandler)
	m.HandleFunc("/api/shorts/analyze", s.shortsAnalyzeHandler)
	m.HandleFunc("/api/shorts/", s.shortsHandler)
	return m
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := staticFS.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) jobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := parseMultipart(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, h, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "selecione um vídeo", http.StatusBadRequest)
		return
	}
	defer f.Close()
	id, dir, input, err := s.storeUpload(h, f, "input")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	audio, err := parseAudioSettings(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	j := &Job{ID: id, Mode: app.ModeAuto, Status: "queued", Stage: "queued", Dir: dir, Source: input, saved: audio}
	s.put(j)
	s.background(func() { s.runAuto(id, input, dir, audio) })
	writeJSON(w, http.StatusAccepted, s.snapshot(id))
}

func (s *Server) reactionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := parseMultipart(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	creator, ch, err := r.FormFile("creator")
	if err != nil {
		http.Error(w, "selecione seu vídeo", http.StatusBadRequest)
		return
	}
	defer creator.Close()
	reaction, rh, err := r.FormFile("reaction")
	if err != nil {
		http.Error(w, "selecione o vídeo para reagir", http.StatusBadRequest)
		return
	}
	defer reaction.Close()
	id := newID()
	dir := filepath.Join(s.cfg.WorkDir, "jobs", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	creatorPath := filepath.Join(dir, "creator"+safeExt(ch))
	reactionPath := filepath.Join(dir, "reaction"+safeExt(rh))
	if err := save(creatorPath, creator); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := save(reactionPath, reaction); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	audioMode := normalizeAudioMode(r.FormValue("audio_mode"))
	j := &Job{ID: id, Mode: app.ModeReaction, Status: "queued", Stage: "queued", Dir: dir, Source: creatorPath}
	s.put(j)
	s.background(func() { s.runReaction(id, creatorPath, reactionPath, audioMode, dir) })
	writeJSON(w, http.StatusAccepted, s.snapshot(id))
}

func (s *Server) shortsAnalyzeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := parseMultipart(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := newID()
	dir := filepath.Join(s.cfg.WorkDir, "jobs", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	url := strings.TrimSpace(r.FormValue("youtube_url"))
	count := shorts.ParseCount(r.FormValue("count"), s.cfg.Shorts.DefaultCount, s.cfg.Shorts.MaxCount)
	var source string
	if f, h, err := r.FormFile("video"); err == nil {
		defer f.Close()
		source = filepath.Join(dir, "long-source"+safeExt(h))
		if err := save(source, f); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	} else if url == "" {
		http.Error(w, "envie um vídeo ou uma URL do YouTube", http.StatusBadRequest)
		return
	}
	j := &Job{ID: id, Mode: "shorts", Status: "queued", Stage: "queued", Dir: dir, Source: source}
	s.put(j)
	s.background(func() { s.runShortsAnalyze(id, source, url, dir, count) })
	writeJSON(w, http.StatusAccepted, s.snapshot(id))
}

func (s *Server) shortsHandler(w http.ResponseWriter, r *http.Request) {
	x := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/shorts/"), "/")
	parts := strings.Split(x, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	j := s.snapshot(id)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	switch action {
	case "generate":
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "JSON inválido", 400)
			return
		}
		selected := make([]shorts.Candidate, 0, len(req.IDs))
		for _, id := range req.IDs {
			if c, ok := shorts.CandidateByID(j.Candidates, id); ok {
				selected = append(selected, c)
			}
		}
		if len(selected) == 0 {
			http.Error(w, "selecione pelo menos um corte", 400)
			return
		}
		s.set(id, func(x *Job) {
			x.Status = "running"
			x.Stage = "shorts.generate"
			x.Percent = 0
			x.Clips = nil
			x.Error = ""
		})
		s.background(func() { s.runShortsGenerate(id, j.Source, j.Dir, selected) })
		writeJSON(w, 202, s.snapshot(id))
	case "preview":
		if r.Method != http.MethodGet || len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		c, ok := shorts.CandidateByID(j.Candidates, parts[2])
		if !ok {
			http.NotFound(w, r)
			return
		}
		previewDir := filepath.Join(j.Dir, "previews")
		_ = os.MkdirAll(previewDir, 0755)
		path := filepath.Join(previewDir, c.ID+".mp4")
		if _, err := os.Stat(path); err != nil {
			if err := shorts.ExtractRange(r.Context(), s.cfg, j.Source, c.Start, c.End, path); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.ServeFile(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) runAuto(id, input, dir string, audio app.AudioSettings) {
	s.set(id, func(j *Job) { j.Status = "running" })
	cfg := s.cfg
	audio.Apply(&cfg)
	res, err := app.Run(context.Background(), cfg, input, dir, s.progress(id))
	if err != nil {
		s.fail(id, err)
		return
	}
	mix := cfg.Music
	s.set(id, func(j *Job) {
		j.Status = "done"
		j.Percent = 100
		j.Result = &res
		j.Output = res.Output
		j.Download = "/api/jobs/" + id + "/download"
		j.Video = "/api/jobs/" + id + "/video"
		j.Audio = &mix
	})
}

// ModeAssembly is the "Montagem IA" job mode.
const ModeAssembly = "assembly"

// assembleHandler receives several clips + a prompt and starts a Montagem IA
// job. Fields: clips (files, repeated), prompt, duration (s), captions
// (auto|on|off) and the audio mixer fields.
func (s *Server) assembleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := parseMultipart(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		http.Error(w, "descreva no prompt o vídeo que você quer", http.StatusBadRequest)
		return
	}
	if len([]rune(prompt)) > 1000 {
		http.Error(w, "prompt longo demais (máx. 1000 caracteres)", http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["clips"]
	if len(files) == 0 {
		http.Error(w, "envie pelo menos um clipe", http.StatusBadRequest)
		return
	}
	if len(files) > s.cfg.Assembly.MaxClips {
		http.Error(w, fmt.Sprintf("máximo de %d clipes por montagem", s.cfg.Assembly.MaxClips), http.StatusBadRequest)
		return
	}
	opts := assembly.Options{Prompt: prompt}
	if v := strings.TrimSpace(r.FormValue("duration")); v != "" && v != "0" {
		d, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(d) || d < 5 || d > 180 {
			http.Error(w, "duração deve estar entre 5 e 180 segundos", http.StatusBadRequest)
			return
		}
		opts.Duration = d
	}
	switch r.FormValue("captions") {
	case "on":
		v := true
		opts.Captions = &v
	case "off":
		v := false
		opts.Captions = &v
	}
	audio, err := parseAudioSettings(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := newID()
	dir := filepath.Join(s.cfg.WorkDir, "jobs", id)
	src := filepath.Join(dir, "sources")
	if err := os.MkdirAll(src, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inputs := make([]assembly.Input, 0, len(files))
	for i, h := range files {
		f, err := h.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		path := filepath.Join(src, fmt.Sprintf("%02d%s", i+1, safeExt(h)))
		err = save(path, f)
		f.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		inputs = append(inputs, assembly.Input{Path: path, Name: displayName(h.Filename)})
	}
	j := &Job{ID: id, Mode: ModeAssembly, Status: "queued", Stage: "queued", Dir: dir, saved: audio}
	s.put(j)
	s.background(func() { s.runAssembly(id, inputs, opts, dir, audio) })
	writeJSON(w, http.StatusAccepted, s.snapshot(id))
}

func (s *Server) runAssembly(id string, inputs []assembly.Input, opts assembly.Options, dir string, audio app.AudioSettings) {
	s.set(id, func(j *Job) { j.Status = "running" })
	cfg := s.cfg
	audio.Apply(&cfg)
	res, err := assembly.Run(context.Background(), cfg, inputs, opts, dir, assembly.Progress(s.progress(id)))
	if err != nil {
		s.fail(id, err)
		return
	}
	mix := cfg.Music
	plan := res.Plan
	s.set(id, func(j *Job) {
		j.Status = "done"
		j.Percent = 100
		j.Output = res.Output
		j.Download = "/api/jobs/" + id + "/download"
		j.Video = "/api/jobs/" + id + "/video"
		j.Audio = &mix
		j.Assembly = &plan
	})
}

// displayName keeps only a safe base name of the uploaded file for the UI.
func displayName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	if n == "." || n == "/" || n == "" {
		return "clipe"
	}
	if r := []rune(n); len(r) > 80 {
		n = string(r[:80])
	}
	return n
}

// remixHandler re-renders only the audio balance of a finished auto job.
func (s *Server) remixHandler(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req app.AudioSettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "JSON de áudio inválido", http.StatusBadRequest)
		return
	}
	if err := validateAudio(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Check-and-start under one lock: two quick clicks never render twice.
	s.mu.Lock()
	j := s.jobs[id]
	if j == nil {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if (j.Mode != app.ModeAuto && j.Mode != ModeAssembly) || j.Status != "done" {
		s.mu.Unlock()
		http.Error(w, "o mixer só funciona em uma edição IA concluída", http.StatusConflict)
		return
	}
	j.Status, j.Stage, j.Percent, j.Error = "running", "mix", 0, ""
	dir, base := j.Dir, j.saved
	s.mu.Unlock()
	s.background(func() { s.runRemix(id, dir, base, req) })
	writeJSON(w, http.StatusAccepted, s.snapshot(id))
}

func (s *Server) runRemix(id, dir string, base, req app.AudioSettings) {
	cfg := s.cfg
	base.Apply(&cfg) // settings chosen at upload time, then the new ones
	res, err := app.Remix(context.Background(), cfg, dir, req, s.progress(id))
	if err != nil {
		s.fail(id, err)
		return
	}
	req.Apply(&cfg)
	mix := cfg.Music
	merged := mergeAudio(base, req)
	s.set(id, func(j *Job) {
		j.Status = "done"
		j.Percent = 100
		j.Output = res.Output
		j.Audio = &mix
		j.saved = merged
	})
}

// mergeAudio keeps base values that the new request does not override.
func mergeAudio(base, next app.AudioSettings) app.AudioSettings {
	out := base
	if next.MusicEnabled != nil {
		out.MusicEnabled = next.MusicEnabled
	}
	if next.MusicDB != nil {
		out.MusicDB = next.MusicDB
	}
	if next.DuckDB != nil {
		out.DuckDB = next.DuckDB
	}
	if next.VoiceDB != nil {
		out.VoiceDB = next.VoiceDB
	}
	if next.SFXDB != nil {
		out.SFXDB = next.SFXDB
	}
	if next.Mood != "" {
		out.Mood = next.Mood
	}
	return out
}

// parseAudioSettings reads the mixer fields of the upload form. Empty fields
// keep the configuration defaults.
func parseAudioSettings(r *http.Request) (app.AudioSettings, error) {
	var a app.AudioSettings
	num := func(name string) (*float64, error) {
		v := strings.TrimSpace(r.FormValue(name))
		if v == "" {
			return nil, nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("valor inválido em %s", name)
		}
		return &f, nil
	}
	var err error
	if a.MusicDB, err = num("music_db"); err != nil {
		return a, err
	}
	if a.DuckDB, err = num("duck_db"); err != nil {
		return a, err
	}
	if a.VoiceDB, err = num("voice_db"); err != nil {
		return a, err
	}
	if a.SFXDB, err = num("sfx_db"); err != nil {
		return a, err
	}
	if v := strings.TrimSpace(r.FormValue("music_enabled")); v != "" {
		on := v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on")
		a.MusicEnabled = &on
	}
	a.Mood = strings.TrimSpace(r.FormValue("mood"))
	return a, validateAudio(a)
}

func validateAudio(a app.AudioSettings) error {
	for _, p := range []*float64{a.MusicDB, a.DuckDB, a.VoiceDB, a.SFXDB} {
		if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < -60 || *p > 60) {
			return fmt.Errorf("valor de áudio fora do intervalo")
		}
	}
	return nil
}

func (s *Server) runReaction(id, creator, reaction, audioMode, dir string) {
	s.set(id, func(j *Job) { j.Status = "running" })
	res, err := app.RunWithOptions(context.Background(), s.cfg, creator, dir, s.progress(id), app.RunOptions{Mode: app.ModeReaction, Reaction: &app.ReactionOptions{Path: reaction, AudioMode: audioMode}})
	if err != nil {
		s.fail(id, err)
		return
	}
	s.set(id, func(j *Job) {
		j.Status = "done"
		j.Percent = 100
		j.Result = &res
		j.Output = res.Output
		j.Download = "/api/jobs/" + id + "/download"
	})
}

func (s *Server) runShortsAnalyze(id, source, url, dir string, count int) {
	s.set(id, func(j *Job) { j.Status = "running" })
	ctx := context.Background()
	if source == "" {
		var err error
		source, err = shorts.DownloadYouTube(ctx, s.cfg, url, filepath.Join(dir, "youtube"), func(stage string, p int, detail string) {
			s.set(id, func(j *Job) { j.Stage = stage; j.Percent = min(15, p); j.Detail = detail })
		})
		if err != nil {
			s.fail(id, err)
			return
		}
		s.set(id, func(j *Job) { j.Source = source })
	}
	analysis, err := shorts.Analyze(ctx, s.cfg, source, filepath.Join(dir, "analysis"), count, func(stage string, p int, detail string) {
		s.set(id, func(j *Job) { j.Stage = "shorts." + stage; j.Percent = 15 + int(float64(p)*.85); j.Detail = detail })
	})
	if err != nil {
		s.fail(id, err)
		return
	}
	s.set(id, func(j *Job) {
		j.Status = "analyzed"
		j.Stage = "shorts.ready"
		j.Percent = 100
		j.Detail = fmt.Sprintf("%d cortes encontrados", len(analysis.Candidates))
		j.Candidates = analysis.Candidates
		j.Source = source
	})
}

func (s *Server) runShortsGenerate(id, source, dir string, selected []shorts.Candidate) {
	ctx := context.Background()
	outputs := make([]ClipOutput, 0, len(selected))
	for i, c := range selected {
		clipDir := filepath.Join(dir, "generated", c.ID)
		generated, err := shorts.Generate(ctx, s.cfg, source, c, clipDir, func(stage string, p int, detail string) {
			base := float64(i) / float64(len(selected))
			within := float64(p) / 100 / float64(len(selected))
			total := int((base + within) * 100)
			s.set(id, func(j *Job) {
				j.Stage = "shorts." + stage
				j.Percent = total
				j.Detail = fmt.Sprintf("%s · %d/%d · %s", c.Title, i+1, len(selected), detail)
			})
		})
		if err != nil {
			s.fail(id, fmt.Errorf("%s: %w", c.ID, err))
			return
		}
		outputs = append(outputs, ClipOutput{ID: c.ID, Title: c.Title, Output: generated.Result.Output, Candidate: c, Download: "/api/jobs/" + id + "/clips/" + c.ID})
	}
	s.set(id, func(j *Job) {
		j.Status = "done"
		j.Stage = "shorts.done"
		j.Percent = 100
		j.Detail = fmt.Sprintf("%d Shorts gerados", len(outputs))
		j.Clips = outputs
	})
}

func (s *Server) jobHandler(w http.ResponseWriter, r *http.Request) {
	x := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	parts := strings.Split(x, "/")
	id := parts[0]
	j := s.snapshot(id)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "remix" {
		s.remixHandler(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "video" {
		if j.Status != "done" || j.Output == "" {
			http.Error(w, "not ready", http.StatusConflict)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, j.Output) // inline, for the preview player
		return
	}
	if len(parts) == 2 && parts[1] == "download" {
		if j.Status != "done" || j.Output == "" {
			http.Error(w, "not ready", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=cenarius-final.mp4")
		http.ServeFile(w, r, j.Output)
		return
	}
	if len(parts) == 3 && parts[1] == "clips" {
		for _, clip := range j.Clips {
			if clip.ID == parts[2] {
				w.Header().Set("Content-Disposition", "attachment; filename=cenarius-"+clip.ID+".mp4")
				http.ServeFile(w, r, clip.Output)
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) progress(id string) app.Progress {
	return func(stage string, p int, d string) {
		s.set(id, func(j *Job) { j.Stage = stage; j.Percent = p; j.Detail = d })
	}
}

func (s *Server) put(j *Job) { s.mu.Lock(); s.jobs[j.ID] = j; s.mu.Unlock() }
func (s *Server) fail(id string, err error) {
	s.set(id, func(j *Job) { j.Status = "error"; j.Error = err.Error() })
}
func (s *Server) set(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		fn(j)
	}
}
func (s *Server) snapshot(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j := s.jobs[id]
	if j == nil {
		return nil
	}
	cp := *j
	cp.Candidates = append([]shorts.Candidate(nil), j.Candidates...)
	cp.Clips = append([]ClipOutput(nil), j.Clips...)
	return &cp
}

func (s *Server) storeUpload(h *multipart.FileHeader, f multipart.File, prefix string) (id, dir, path string, err error) {
	id = newID()
	dir = filepath.Join(s.cfg.WorkDir, "jobs", id)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return
	}
	path = filepath.Join(dir, prefix+safeExt(h))
	err = save(path, f)
	return
}

func parseMultipart(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)
	return r.ParseMultipartForm(64 << 20)
}

func newID() string { b := make([]byte, 8); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func safeExt(h *multipart.FileHeader) string {
	ext := strings.ToLower(filepath.Ext(h.Filename))
	switch ext {
	case ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return ext
	}
	return ".mp4"
}
func save(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
func normalizeAudioMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "muted":
		return "muted"
	case "original":
		return "original"
	default:
		return "duck"
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Listen(addr string, cfg config.Config) error {
	fmt.Printf("CENARIUS Studio: http://%s\n", addr)
	return http.ListenAndServe(addr, New(cfg).Handler())
}
