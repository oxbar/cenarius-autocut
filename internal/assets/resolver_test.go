package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
)

func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// needFFmpeg returns the best local ffmpeg/ffprobe (FFMPEG_BIN, Homebrew
// ffmpeg-full, PATH) or skips.
func needFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	ff, fp := FindFFmpeg()
	if ff == "" || fp == "" {
		t.Skip("ffmpeg/ffprobe não disponível")
	}
	return ff, fp
}

// needTextFFmpeg also requires a way to draw text (drawtext or libass).
func needTextFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	ff, fp := needFFmpeg(t)
	if !FFmpegHasFilter(ff, "drawtext") && !FFmpegHasFilter(ff, "ass") {
		t.Skipf("%s não tem drawtext nem ass (use ffmpeg-full ou FFMPEG_BIN)", ff)
	}
	return ff, fp
}

func makeVideo(t *testing.T, path string, seconds string) {
	t.Helper()
	ff, _ := needFFmpeg(t)
	cmd := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration="+seconds, "-pix_fmt", "yuv420p", "-c:v", "libx264", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

// fakeCommons simulates the Commons API: results[search] -> pages, files are
// served from /media/<name>. Downloads are counted.
type fakeCommons struct {
	srv       *httptest.Server
	results   map[string][]map[string]any
	files     map[string][]byte
	types     map[string]string
	downloads int32
	searches  []string
}

func newFakeCommons(t *testing.T) *fakeCommons {
	f := &fakeCommons{results: map[string][]map[string]any{}, files: map[string][]byte{}, types: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/media/") {
			name := strings.TrimPrefix(r.URL.Path, "/media/")
			b, ok := f.files[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			atomic.AddInt32(&f.downloads, 1)
			w.Header().Set("Content-Type", f.types[name])
			_, _ = w.Write(b)
			return
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if q.Get("prop") == "videoinfo" {
			_, _ = w.Write([]byte(`{"query":{"pages":[]}}`))
			return
		}
		s := q.Get("gsrsearch")
		f.searches = append(f.searches, s)
		pages := f.results[s]
		if pages == nil {
			pages = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": pages}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCommons) page(title, file, mime string) map[string]any {
	u := f.srv.URL + "/media/" + file
	return map[string]any{"pageid": 1, "title": title, "imageinfo": []map[string]any{{
		"url": u, "thumburl": u, "mime": mime, "size": 1000, "width": 1280, "height": 720,
		"extmetadata": map[string]any{"LicenseShortName": map[string]string{"value": "CC BY-SA 4.0"}, "Artist": map[string]string{"value": "<a>Example Author</a>"}},
	}}}
}

func newTestResolver(t *testing.T, f *fakeCommons) *Resolver {
	d := t.TempDir()
	r := New(filepath.Join(d, "assets"), filepath.Join(d, "none.json"), filepath.Join(d, "cache"))
	r.Remote = true
	if f != nil {
		r.API = f.srv.URL + "/w/api.php"
		r.Client = f.srv.Client()
	}
	return r
}

func TestResolveLocal(t *testing.T) {
	d := t.TempDir()
	assetDir := filepath.Join(d, "assets")
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(assetDir, "java.png")
	if err := os.WriteFile(img, realPNG(t, 320, 200), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(d, "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"assets":[{"keyword":"java","aliases":["spring"],"file":"java.png"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	r := New(assetDir, manifest, filepath.Join(d, "cache"))
	r.Remote = false
	a, err := r.Resolve(context.Background(), "java", "Java programming language")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "local" || a.Path != img {
		t.Fatalf("unexpected asset: %+v", a)
	}
}

func TestLocalInvalidFileIsSkipped(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "java.png"), []byte("fake"), 0644)
	manifest := filepath.Join(d, "manifest.json")
	_ = os.WriteFile(manifest, []byte(`{"assets":[{"keyword":"java","file":"java.png"}]}`), 0644)
	r := New(d, manifest, filepath.Join(d, "cache"))
	r.Remote = false
	if _, err := r.Resolve(context.Background(), "java", "java"); err == nil {
		t.Fatal("invalid local file must not resolve")
	}
}

func TestResolveCommonsAndCache(t *testing.T) {
	f := newFakeCommons(t)
	f.files["tech.png"], f.types["tech.png"] = realPNG(t, 640, 400), "image/png"
	f.results["computer technology filetype:bitmap"] = []map[string]any{f.page("File:Computer technology.png", "tech.png", "image/png")}
	r := newTestResolver(t, f)
	a, err := r.Resolve(context.Background(), "tecnologia", "computer technology")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "wikimedia-commons" || a.License != "CC BY-SA 4.0" || a.Author != "Example Author" || a.SourceURL == "" || a.URL == "" {
		t.Fatalf("unexpected: %+v", a)
	}
	r.Remote = false // second call must come from cache
	cached, err := r.Resolve(context.Background(), "tecnologia", "computer technology")
	if err != nil {
		t.Fatal(err)
	}
	if cached.Path != a.Path || cached.Source != SourceCache || cached.Origin != SourceCommons {
		t.Fatalf("cache mismatch: %+v", cached)
	}
}

func TestAlternativeQueries(t *testing.T) {
	f := newFakeCommons(t)
	f.files["ide.png"], f.types["ide.png"] = realPNG(t, 640, 400), "image/png"
	// query 1 has nothing (neither video nor image); query 2 has an image.
	f.results["programmer coding IDE filetype:bitmap"] = []map[string]any{f.page("File:Programmer coding in an IDE.png", "ide.png", "image/png")}
	r := newTestResolver(t, f)
	r.Procedural, r.AllowSelfBroll = false, false
	ev := planner.VisualEvent{ID: "ve-001", Concept: "software development", Layout: planner.LayoutReaction,
		Queries: []string{"software developer programming computer", "programmer coding IDE", "software development computer screen"}}
	ref, a, err := r.ResolveEvent(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != SourceCommons || a.Query != "programmer coding IDE" || ref.Fallback {
		t.Fatalf("second query not used: %+v %+v", ref, a)
	}
	// video tier must have been tried for query 1 before images.
	if len(f.searches) == 0 || f.searches[0] != "software developer programming computer filetype:video" {
		t.Fatalf("video must be preferred first, searches=%v", f.searches)
	}
}

func TestCacheNeverDownloadsTwice(t *testing.T) {
	f := newFakeCommons(t)
	f.files["server.png"], f.types["server.png"] = realPNG(t, 640, 400), "image/png"
	f.results["server rack filetype:bitmap"] = []map[string]any{f.page("File:Server rack.png", "server.png", "image/png")}
	f.results["data center servers filetype:bitmap"] = []map[string]any{f.page("File:Data center servers rack.png", "server.png", "image/png")}
	r := newTestResolver(t, f)
	r.Procedural, r.AllowSelfBroll = false, false
	ctx := context.Background()
	ev := planner.VisualEvent{Concept: "servers", Queries: []string{"server rack"}, Layout: planner.LayoutReaction}
	if _, _, err := r.ResolveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	ref2, _, err := r.ResolveEvent(ctx, ev)
	if err != nil || ref2.Source != SourceCache {
		t.Fatalf("second resolve must be a cache hit: %+v %v", ref2, err)
	}
	// Different query, same media URL: media-level cache, no new download.
	if _, _, err := r.ResolveEvent(ctx, planner.VisualEvent{Concept: "servers", Queries: []string{"data center servers"}, Layout: planner.LayoutReaction}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&f.downloads); n != 1 {
		t.Fatalf("file downloaded %d times", n)
	}
}

func TestRejectsHTMLAndTriesNextCandidate(t *testing.T) {
	f := newFakeCommons(t)
	f.files["broken.png"], f.types["broken.png"] = []byte("<!DOCTYPE html><html><body>Error 403</body></html>"), "image/png"
	f.files["trunc.png"], f.types["trunc.png"] = realPNG(t, 640, 400)[:200], "image/png"
	f.files["good.png"], f.types["good.png"] = realPNG(t, 640, 400), "image/png"
	f.results["laptop computer filetype:bitmap"] = []map[string]any{
		f.page("File:Laptop computer 1.png", "broken.png", "image/png"),
		f.page("File:Laptop computer 2.png", "trunc.png", "image/png"),
		f.page("File:Laptop computer 3.png", "good.png", "image/png"),
	}
	r := newTestResolver(t, f)
	r.Procedural, r.AllowSelfBroll = false, false
	_, a, err := r.ResolveEvent(context.Background(), planner.VisualEvent{Concept: "computer", Queries: []string{"laptop computer"}, Layout: planner.LayoutReaction})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(a.URL, "good.png") {
		t.Fatalf("invalid candidates were accepted: %+v", a)
	}
	matches, _ := filepath.Glob(filepath.Join(r.CacheDir, "media", "*"))
	if len(matches) != 1 {
		t.Fatalf("rejected downloads left in cache: %v", matches)
	}
}

func TestIrrelevantOrUnlicensedCandidatesRejected(t *testing.T) {
	f := newFakeCommons(t)
	f.files["x.png"], f.types["x.png"] = realPNG(t, 640, 400), "image/png"
	bad := f.page("File:Cathedral of Florence.png", "x.png", "image/png")
	nc := f.page("File:Programmer coding.png", "x.png", "image/png")
	nc["imageinfo"].([]map[string]any)[0]["extmetadata"] = map[string]any{"LicenseShortName": map[string]string{"value": "CC BY-NC 4.0"}}
	logo := f.page("File:Programmer coding logo.png", "x.png", "image/png")
	f.results["programmer coding filetype:bitmap"] = []map[string]any{bad, nc, logo}
	r := newTestResolver(t, f)
	r.Procedural, r.AllowSelfBroll = false, false
	if _, _, err := r.ResolveEvent(context.Background(), planner.VisualEvent{Concept: "dev", Queries: []string{"programmer coding"}, Layout: planner.LayoutReaction}); err == nil {
		t.Fatal("irrelevant / NC / logo candidates must be rejected for B-roll")
	}
}

func TestFallbackOrderSelfBrollIsLast(t *testing.T) {
	ctx := context.Background()
	ev := planner.VisualEvent{ID: "ve-001", Concept: "technology", Label: "TECNOLOGIA", Queries: []string{"computer technology"}, Layout: planner.LayoutReaction}

	r := newTestResolver(t, nil)
	r.Remote = false
	r.Procedural = false
	ref, a, err := r.ResolveEvent(ctx, ev)
	if err != nil || ref.Source != SourceSelfBroll || !ref.Fallback || a != nil {
		t.Fatalf("self-broll must be a flagged fallback without attribution: %+v %+v %v", ref, a, err)
	}

	r.AllowSelfBroll = false
	if _, _, err := r.ResolveEvent(ctx, ev); err == nil {
		t.Fatal("without self-broll an unresolved event must error (and be dropped)")
	}

	ff, fp := needTextFFmpeg(t)
	r2 := newTestResolver(t, nil)
	r2.Remote = false
	r2.FFmpeg = ff
	r2.Validator = FFValidator{FFprobe: fp, FFmpeg: ff}
	ref, a, err = r2.ResolveEvent(ctx, ev)
	if err != nil || ref.Source != SourceProcedure || ref.Type != "video" || a == nil {
		t.Fatalf("procedural must come before self-broll: %+v %v", ref, err)
	}
}

func TestResolvePlanWarnsWhenSelfBrollDominates(t *testing.T) {
	dir := t.TempDir()
	ctx, logPath, closer, err := logx.StartJob(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newTestResolver(t, nil)
	r.Remote, r.Procedural = false, false
	p := planner.Plan{VisualEvents: []planner.VisualEvent{
		{ID: "ve-001", Concept: "a", Queries: []string{"a"}, Start: 2, End: 4, Layout: planner.LayoutReaction},
		{ID: "ve-002", Concept: "b", Queries: []string{"b"}, Start: 6, End: 8, Layout: planner.LayoutReaction},
	}}
	out, attr := r.ResolvePlan(ctx, p)
	closer.Close()
	if len(out.VisualEvents) != 2 || len(attr) != 0 {
		t.Fatalf("%+v %+v", out, attr)
	}
	b, _ := os.ReadFile(logPath)
	for _, want := range []string{"visual.asset.self_broll_majority", "fallback=self-broll", "level=WARN"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("debug.log missing %q:\n%s", want, b)
		}
	}
}

func TestLocalBeatsEveryFallback(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "tech.png"), realPNG(t, 320, 200), 0644)
	manifest := filepath.Join(d, "manifest.json")
	_ = os.WriteFile(manifest, []byte(`{"assets":[{"keyword":"tecnologia","concepts":["technology"],"file":"tech.png","license":"CC0"}]}`), 0644)
	r := New(d, manifest, filepath.Join(d, "cache"))
	r.Remote = false
	ref, a, err := r.ResolveEvent(context.Background(), planner.VisualEvent{Concept: "technology", Queries: []string{"computer technology"}, Layout: planner.LayoutReaction})
	if err != nil || ref.Source != SourceLocal || ref.Fallback || a.License != "CC0" {
		t.Fatalf("%+v %+v %v", ref, a, err)
	}
}

func TestImageValidation(t *testing.T) {
	d := t.TempDir()
	v := FFValidator{}
	ok := filepath.Join(d, "ok.png")
	_ = os.WriteFile(ok, realPNG(t, 200, 120), 0644)
	if m, err := v.Validate(context.Background(), ok, "image"); err != nil || m.Width != 200 {
		t.Fatalf("valid png rejected: %v %+v", err, m)
	}
	cases := map[string][]byte{
		"html.png":  []byte("<!DOCTYPE html><html><head><title>404</title></head><body>not found</body></html>"),
		"trunc.png": realPNG(t, 200, 120)[:180],
		"tiny.png":  realPNG(t, 16, 16),
		"junk.jpg":  bytes.Repeat([]byte{0x13, 0x37}, 200),
	}
	for name, b := range cases {
		p := filepath.Join(d, name)
		_ = os.WriteFile(p, b, 0644)
		if _, err := v.Validate(context.Background(), p, "image"); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestVideoValidation(t *testing.T) {
	ff, fp := needFFmpeg(t)
	d := t.TempDir()
	v := FFValidator{FFprobe: fp, FFmpeg: ff}
	ok := filepath.Join(d, "ok.mp4")
	makeVideo(t, ok, "2")
	m, err := v.Validate(context.Background(), ok, "video")
	if err != nil || m.Width != 640 || m.Duration < 1.9 {
		t.Fatalf("valid video rejected: %v %+v", err, m)
	}
	html := filepath.Join(d, "html.mp4")
	_ = os.WriteFile(html, []byte("<html><body>Too many requests</body></html>"+strings.Repeat(" ", 100)), 0644)
	junk := filepath.Join(d, "junk.webm")
	_ = os.WriteFile(junk, bytes.Repeat([]byte{0x1A, 0x45, 0xDF, 0xA3, 0, 1, 2, 3}, 64), 0644)
	short := filepath.Join(d, "short.mp4")
	makeVideo(t, short, "0.2")
	for _, p := range []string{html, junk, short} {
		if _, err := v.Validate(context.Background(), p, "video"); err == nil {
			t.Fatalf("%s must be rejected", filepath.Base(p))
		}
	}
}

func TestLicenseOK(t *testing.T) {
	for _, l := range []string{"CC BY-SA 4.0", "CC BY 3.0", "CC0", "Public domain", "PD-USGov", "cc-by-sa-3.0"} {
		if !LicenseOK(l) {
			t.Fatalf("%s should be accepted", l)
		}
	}
	for _, l := range []string{"", "CC BY-NC 4.0", "CC BY-ND 2.0", "CC BY-NC-SA 3.0", "All rights reserved", "Fair use"} {
		if LicenseOK(l) {
			t.Fatalf("%s should be rejected", l)
		}
	}
}

func TestProceduralMotionGraphic(t *testing.T) {
	ff, fp := needTextFFmpeg(t)
	out := filepath.Join(t.TempDir(), "m.mp4")
	if err := GenerateMotion(context.Background(), ff, "INTELIGÊNCIA ARTIFICIAL", out, 1080, 1920, 1.5); err != nil {
		t.Fatal(err)
	}
	m, err := FFValidator{FFprobe: fp, FFmpeg: ff}.Validate(context.Background(), out, "video")
	if err != nil || m.Width != 1080 || m.Height != 1920 {
		t.Fatalf("%v %+v", err, m)
	}
}

// Homebrew's plain ffmpeg has libass but no drawtext: the motion graphic must
// still be produced (label drawn with libass).
func TestProceduralWithoutDrawtextUsesLibass(t *testing.T) {
	ff, fp := needFFmpeg(t)
	if !hasFilter(ff, "ass") {
		t.Skip("ffmpeg sem libass")
	}
	orig := HasFilterFunc
	HasFilterFunc = func(bin, name string) bool { return name != "drawtext" && hasFilter(bin, name) }
	defer func() { HasFilterFunc = orig }()
	out := filepath.Join(t.TempDir(), "m.mp4")
	if err := GenerateMotion(context.Background(), ff, "SEGURANÇA", out, 1080, 1920, 1.2); err != nil {
		t.Fatal(err)
	}
	if _, err := (FFValidator{FFprobe: fp, FFmpeg: ff}).Validate(context.Background(), out, "video"); err != nil {
		t.Fatal(err)
	}
}

func TestProceduralWithoutTextFiltersFailsClearly(t *testing.T) {
	orig := HasFilterFunc
	HasFilterFunc = func(string, string) bool { return false }
	defer func() { HasFilterFunc = orig }()
	err := GenerateMotion(context.Background(), "ffmpeg", "X", filepath.Join(t.TempDir(), "m.mp4"), 1080, 1920, 1)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg-full") {
		t.Fatalf("want actionable error, got %v", err)
	}
}

type stubValidator struct {
	media Media
}

func (v stubValidator) Validate(ctx context.Context, path, kind string) (Media, error) {
	m := v.media
	if m.Type == "" {
		m.Type = kind
	}
	if m.Width == 0 {
		m.Width = 1080
	}
	if m.Height == 0 {
		m.Height = 1920
	}
	if kind == "video" && m.Duration == 0 {
		m.Duration = 8
	}
	return m, nil
}

func TestPexelsPreferredBeforePixabayAndSecretsStayOutOfLog(t *testing.T) {
	var pixabayCalls int32
	secret := "pexels-super-secret-key"
	media := bytes.Repeat([]byte{0x42}, 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/pexels/videos/search":
			if got := r.Header.Get("Authorization"); got != secret {
				t.Errorf("missing pexels auth header: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"videos":[{"id":7,"width":1080,"height":1920,"url":"https://www.pexels.com/video/7/","duration":8,"user":{"name":"Creator"},"video_files":[{"quality":"hd","file_type":"video/mp4","width":1080,"height":1920,"link":"` + "http://" + strings.TrimPrefix(r.Host, "") + `/media/pexels.mp4"}]}]}`))
		case r.URL.Path == "/pixabay/api/videos/":
			atomic.AddInt32(&pixabayCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"hits":[]}`))
		case r.URL.Path == "/media/pexels.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(media)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, logPath, closer, err := logx.StartJob(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	r := New(filepath.Join(dir, "assets"), filepath.Join(dir, "none.json"), filepath.Join(dir, "cache"))
	r.Client = srv.Client()
	r.PexelsAPI = srv.URL + "/pexels"
	r.PixabayAPI = srv.URL + "/pixabay/api"
	r.PexelsKey = secret
	r.PixabayKey = "pixabay-secret"
	r.API = srv.URL + "/commons"
	r.Validator = stubValidator{media: Media{Type: "video", Width: 1080, Height: 1920, Duration: 8}}
	r.Procedural, r.AllowSelfBroll = false, false
	ev := planner.VisualEvent{ID: "ve-001", Concept: "software development", Queries: []string{"programmer coding"}, Layout: planner.LayoutReaction}
	ref, a, err := r.ResolveEvent(ctx, ev)
	closer.Close()
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != SourcePexels || a == nil || a.License != "Pexels License" {
		t.Fatalf("expected Pexels, got ref=%+v asset=%+v", ref, a)
	}
	if atomic.LoadInt32(&pixabayCalls) != 0 {
		t.Fatalf("Pixabay should not be queried after Pexels success")
	}
	b, _ := os.ReadFile(logPath)
	if bytes.Contains(b, []byte(secret)) || bytes.Contains(b, []byte("pixabay-secret")) {
		t.Fatalf("API key leaked to debug.log:\n%s", b)
	}
}

func TestProviderFallbackPexelsToPixabay(t *testing.T) {
	media := bytes.Repeat([]byte{0x24}, 1024)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pexels/videos/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"videos":[]}`))
		case "/pixabay/api/videos/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"hits":[{"id":9,"pageURL":"https://pixabay.com/videos/id-9/","duration":7,"user":"PixUser","tags":"programming, code","videos":{"medium":{"url":"` + srv.URL + `/media/pixabay.mp4","width":1080,"height":1920,"size":1024}}}]}`))
		case "/media/pixabay.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(media)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := t.TempDir()
	r := New(filepath.Join(d, "assets"), filepath.Join(d, "none.json"), filepath.Join(d, "cache"))
	r.Client = srv.Client()
	r.PexelsAPI = srv.URL + "/pexels"
	r.PixabayAPI = srv.URL + "/pixabay/api"
	r.PexelsKey = "pexels-key"
	r.PixabayKey = "pixabay-key"
	r.API = srv.URL + "/commons"
	r.Validator = stubValidator{media: Media{Type: "video", Width: 1080, Height: 1920, Duration: 7}}
	r.Procedural, r.AllowSelfBroll = false, false
	ref, a, err := r.ResolveEvent(context.Background(), planner.VisualEvent{ID: "ve-001", Concept: "software development", Queries: []string{"programmer coding"}, Layout: planner.LayoutReaction})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Source != SourcePixabay || a == nil || a.License != "Pixabay Content License" {
		t.Fatalf("expected Pixabay fallback, got ref=%+v asset=%+v", ref, a)
	}
}

func TestResolvePlanDoesNotReuseSameLocalAssetWhenAlternativeExists(t *testing.T) {
	d := t.TempDir()
	assetDir := filepath.Join(d, "assets")
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.png", "two.png"} {
		if err := os.WriteFile(filepath.Join(assetDir, name), realPNG(t, 640, 960), 0644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(d, "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"assets":[
		{"keyword":"programacao","concepts":["software development"],"file":"one.png","license":"CC0"},
		{"keyword":"programacao","concepts":["software development"],"file":"two.png","license":"CC0"}
	]}`), 0644); err != nil {
		t.Fatal(err)
	}
	r := New(assetDir, manifest, filepath.Join(d, "cache"))
	r.Remote, r.Procedural, r.AllowSelfBroll = false, false, false
	p := planner.Plan{VisualEvents: []planner.VisualEvent{
		{ID: "ve-001", Concept: "software development", Queries: []string{"programacao"}, Start: 2, End: 4, Layout: planner.LayoutReaction},
		{ID: "ve-002", Concept: "software development", Queries: []string{"programacao"}, Start: 7, End: 9, Layout: planner.LayoutReaction},
	}}
	out, _ := r.ResolvePlan(context.Background(), p)
	if len(out.VisualEvents) != 2 {
		t.Fatalf("expected both events resolved, got %+v", out.VisualEvents)
	}
	if out.VisualEvents[0].Asset.Path == out.VisualEvents[1].Asset.Path {
		t.Fatalf("same local asset reused: %s", out.VisualEvents[0].Asset.Path)
	}
}

func TestStockShapeScorePrefersVerticalShortsMedia(t *testing.T) {
	vertical := stockShapeScore(1080, 1920, 8)
	horizontal := stockShapeScore(1920, 1080, 8)
	if vertical <= horizontal {
		t.Fatalf("vertical score=%v must beat horizontal score=%v", vertical, horizontal)
	}
}

func TestWriteAttributionsPreservesProviderMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assets-attribution.json")
	want := Asset{
		EventID: "ve-001", Keyword: "software development", Query: "programmer coding",
		Path: "/tmp/clip.mp4", Source: SourcePexels, MediaType: "video",
		URL: "https://cdn.example/clip.mp4", SourceURL: "https://www.pexels.com/video/7/",
		License: "Pexels License", Author: "Creator", Attribution: "Pexels",
	}
	if err := WriteAttributions(path, []Asset{want}); err != nil {
		t.Fatal(err)
	}
	var got []Asset
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != SourcePexels || got[0].License != want.License || got[0].Author != want.Author || got[0].SourceURL != want.SourceURL {
		t.Fatalf("attribution metadata lost: %+v", got)
	}
}

type staticValidator struct {
	media Media
	err   error
}

func (v staticValidator) Validate(context.Context, string, string) (Media, error) {
	return v.media, v.err
}

func TestResolvePlanPreservesManualReactionAsset(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "reaction.mp4")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 1024), 0644); err != nil {
		t.Fatal(err)
	}
	r := New(d, filepath.Join(d, "none.json"), filepath.Join(d, "cache"))
	r.Remote, r.Procedural, r.AllowSelfBroll = false, false, false
	r.Validator = staticValidator{media: Media{Type: "video", Width: 1920, Height: 1080, Duration: 42}}
	p := planner.Plan{VisualEvents: []planner.VisualEvent{{ID: "reaction-001", Start: 0, End: 20, Concept: "manual reaction", Layout: planner.LayoutReaction, Asset: &planner.AssetRef{Path: path, Type: "video", Source: planner.SourceManual, AudioMode: "duck"}}}}
	out, attrs := r.ResolvePlan(context.Background(), p)
	if len(out.VisualEvents) != 1 || out.VisualEvents[0].Asset.Source != planner.SourceManual || out.VisualEvents[0].Asset.AudioMode != "duck" {
		t.Fatalf("manual asset was not preserved: %+v", out.VisualEvents)
	}
	if len(attrs) != 1 || attrs[0].Source != planner.SourceManual || attrs[0].License != "user-provided" {
		t.Fatalf("manual attribution missing: %+v", attrs)
	}
}
