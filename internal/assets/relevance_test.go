package assets

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cenarius-autocut/internal/planner"
)

func TestSlugText(t *testing.T) {
	cases := map[string]string{
		"https://www.pexels.com/video/a-man-typing-on-a-laptop-3129671/": "a man typing on a laptop",
		"https://pixabay.com/videos/code-programming-computer-12345/":    "code programming computer",
		"https://www.pexels.com/video/7/":                                "",
	}
	for in, want := range cases {
		if got := slugText(in); got != want {
			t.Fatalf("slugText(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSemanticScoreGate(t *testing.T) {
	anchors := planner.ConceptAnchors("software development")
	if _, ok := SemanticScore("software developer programming computer", anchors, "sunset over the ocean waves"); ok {
		t.Fatal("off-topic stock result must be rejected")
	}
	on, ok := SemanticScore("software developer programming computer", anchors, "man typing on a laptop")
	if !ok || on < 0.5 {
		t.Fatalf("on-topic result scored %.2f ok=%v", on, ok)
	}
	strong, _ := SemanticScore("software developer programming computer", anchors, "programmer coding on laptop keyboard screen")
	if strong <= on {
		t.Fatalf("more anchor evidence must score higher: %.2f <= %.2f", strong, on)
	}
	if s, ok := SemanticScore("x", anchors, ""); !ok || s >= on {
		t.Fatalf("unknown description must be allowed but ranked below proven matches: %.2f", s)
	}
}

// The v1.7 behaviour took Pexels result #1 blindly. Now an off-topic #1
// (popular sunset clip) must lose to an on-topic #2.
func TestPexelsOffTopicFirstResultIsSkipped(t *testing.T) {
	media := bytes.Repeat([]byte{0x42}, 1024)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pexels/videos/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"videos":[
			 {"id":1,"width":1080,"height":1920,"url":"https://www.pexels.com/video/sunset-over-the-ocean-1/","duration":8,"user":{"name":"A"},
			  "video_files":[{"quality":"hd","file_type":"video/mp4","width":1080,"height":1920,"link":"` + srv.URL + `/media/sunset.mp4"}]},
			 {"id":2,"width":1080,"height":1920,"url":"https://www.pexels.com/video/programmer-typing-code-on-laptop-2/","duration":8,"user":{"name":"B"},
			  "video_files":[{"quality":"hd","file_type":"video/mp4","width":1080,"height":1920,"link":"` + srv.URL + `/media/code.mp4"}]}]}`))
		case "/media/sunset.mp4", "/media/code.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(media)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	r := New(filepath.Join(dir, "assets"), filepath.Join(dir, "none.json"), filepath.Join(dir, "cache"))
	r.Client, r.PexelsAPI, r.PexelsKey, r.PixabayKey = srv.Client(), srv.URL+"/pexels", "k", ""
	r.API = srv.URL + "/commons"
	r.Validator = stubValidator{media: Media{Type: "video", Width: 1080, Height: 1920, Duration: 8}}
	r.Procedural, r.AllowSelfBroll = false, false
	ev := planner.VisualEvent{ID: "ve-001", Concept: "software development", Anchors: planner.ConceptAnchors("software development"),
		Queries: []string{"software developer programming computer"}, Layout: planner.LayoutReaction}
	_, a, err := r.ResolveEvent(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(a.URL, "/media/code.mp4") {
		t.Fatalf("off-topic popular clip chosen: %s", a.URL)
	}
}
