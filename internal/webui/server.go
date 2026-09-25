package webui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cenarius-autocut/internal/app"
	"cenarius-autocut/internal/config"
)

//go:embed static/*
var staticFS embed.FS

type Job struct {
	ID, Status, Stage, Detail, Output string
	Percent                           int
	Error                             string
	Result                            *app.Result
}
type Server struct {
	cfg  config.Config
	mu   sync.RWMutex
	jobs map[string]*Job
}

func New(cfg config.Config) *Server { return &Server{cfg: cfg, jobs: map[string]*Job{}} }
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.index)
	m.HandleFunc("/api/jobs", s.jobsHandler)
	m.HandleFunc("/api/jobs/", s.jobHandler)
	return m
}
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, _ := staticFS.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}
func (s *Server) jobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f, h, err := r.FormFile("video")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer f.Close()
	id := newID()
	dir := filepath.Join(s.cfg.WorkDir, "jobs", id)
	os.MkdirAll(dir, 0755)
	name := safeName(h)
	input := filepath.Join(dir, name)
	if err := save(input, f); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	j := &Job{ID: id, Status: "queued", Stage: "queued"}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	go s.run(id, input, dir)
	writeJSON(w, 202, j)
}
func (s *Server) run(id, input, dir string) {
	ctx := context.Background()
	s.set(id, func(j *Job) { j.Status = "running" })
	res, err := app.Run(ctx, s.cfg, input, dir, func(stage string, p int, d string) {
		s.set(id, func(j *Job) { j.Stage = stage; j.Percent = p; j.Detail = d })
	})
	if err != nil {
		s.set(id, func(j *Job) { j.Status = "error"; j.Error = err.Error() })
		return
	}
	s.set(id, func(j *Job) { j.Status = "done"; j.Percent = 100; j.Result = &res; j.Output = res.Output })
}
func (s *Server) jobHandler(w http.ResponseWriter, r *http.Request) {
	x := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(x, "/")
	id := parts[0]
	s.mu.RLock()
	j, ok := s.jobs[id]
	if ok {
		cp := *j
		j = &cp
	}
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "download" {
		if j.Status != "done" {
			http.Error(w, "not ready", 409)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=cenarius-final.mp4")
		http.ServeFile(w, r, j.Output)
		return
	}
	writeJSON(w, 200, j)
}
func (s *Server) set(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		fn(j)
	}
}
func newID() string { b := make([]byte, 8); rand.Read(b); return hex.EncodeToString(b) }
func safeName(h *multipart.FileHeader) string {
	ext := strings.ToLower(filepath.Ext(h.Filename))
	switch ext {
	case ".mp4", ".mov", ".m4v", ".webm":
	default:
		ext = ".mp4"
	}
	return "input" + ext
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
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Listen(addr string, cfg config.Config) error {
	fmt.Printf("CENARIUS AutoCut: http://%s\n", addr)
	return http.ListenAndServe(addr, New(cfg).Handler())
}
