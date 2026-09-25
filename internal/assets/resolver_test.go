package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocal(t *testing.T) {
	d := t.TempDir()
	assetDir := filepath.Join(d, "assets")
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(assetDir, "java.png")
	if err := os.WriteFile(img, []byte("fake"), 0644); err != nil {
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

func TestResolveCommonsAndCache(t *testing.T) {
	png := []byte("PNGDATA")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/img.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":{"pages":[{"pageid":1,"title":"File:Technology.png","imageinfo":[{"url":"` + srv.URL + `/img.png","thumburl":"` + srv.URL + `/img.png","mime":"image/png","extmetadata":{"LicenseShortName":{"value":"CC BY-SA 4.0"},"Artist":{"value":"Example Author"}}}]}]}}`))
	}))
	defer srv.Close()

	d := t.TempDir()
	r := New(filepath.Join(d, "assets"), filepath.Join(d, "none.json"), filepath.Join(d, "cache"))
	r.API = srv.URL
	r.Client = srv.Client()
	a, err := r.Resolve(context.Background(), "tecnologia", "computer technology")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "wikimedia-commons" || a.License != "CC BY-SA 4.0" {
		t.Fatalf("unexpected: %+v", a)
	}
	b, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(png) {
		t.Fatalf("download mismatch: %q", b)
	}

	// Turn remote off: the second call must hit the local cache.
	r.Remote = false
	cached, err := r.Resolve(context.Background(), "tecnologia", "computer technology")
	if err != nil {
		t.Fatal(err)
	}
	if cached.Path != a.Path || cached.Source == "" {
		t.Fatalf("cache mismatch: %+v", cached)
	}
}
