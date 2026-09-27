package webui

import (
	"net/http/httptest"
	"strings"
	"testing"

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
