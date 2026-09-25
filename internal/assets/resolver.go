package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
)

const defaultCommonsAPI = "https://commons.wikimedia.org/w/api.php"

type Asset struct {
	Keyword     string `json:"keyword"`
	Query       string `json:"query,omitempty"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url,omitempty"`
	License     string `json:"license,omitempty"`
	Author      string `json:"author,omitempty"`
	Attribution string `json:"attribution,omitempty"`
}

type manifestAsset struct {
	Keyword string   `json:"keyword"`
	Aliases []string `json:"aliases"`
	File    string   `json:"file"`
}

type manifest struct {
	Assets []manifestAsset `json:"assets"`
}

type Resolver struct {
	AssetDir string
	Manifest string
	CacheDir string
	API      string
	Client   *http.Client
	Remote   bool
}

func New(assetDir, manifestPath, cacheDir string) *Resolver {
	remote := os.Getenv("CENARIUS_NO_REMOTE_ASSETS") != "1"
	return &Resolver{
		AssetDir: assetDir,
		Manifest: manifestPath,
		CacheDir: cacheDir,
		API:      defaultCommonsAPI,
		Client:   &http.Client{Timeout: 18 * time.Second},
		Remote:   remote,
	}
}

// ResolvePlan resolves each planned overlay to a local file. Resolution order:
// local manifest -> cache -> Wikimedia Commons. Nothing here requires a paid API.
// If resolution fails, the overlay remains in the plan with an empty Asset field;
// the renderer will still create a branded context card so the visual event is not lost.
func (r *Resolver) ResolvePlan(ctx context.Context, p planner.Plan) (planner.Plan, []Asset) {
	logger := logx.From(ctx)
	if err := os.MkdirAll(r.CacheDir, 0755); err != nil {
		logger.Warn("visual.cache.mkdir_failed", "dir", r.CacheDir, "error", err)
	}
	out := p
	resolved := make([]Asset, 0, len(out.Overlays))
	for i := range out.Overlays {
		e := &out.Overlays[i]
		query := strings.TrimSpace(e.Query)
		if query == "" {
			query = strings.TrimSpace(e.Keyword)
		}
		logger.Info("visual.asset.query", "index", i, "keyword", e.Keyword, "query", query, "mode", e.Mode)
		a, err := r.Resolve(ctx, e.Keyword, query)
		if err != nil {
			logger.Warn("visual.asset.unresolved", "index", i, "keyword", e.Keyword, "query", query, "error", err)
			e.Asset = ""
			e.AssetSource = "generated-card"
			continue
		}
		e.Asset = a.Path
		e.AssetSource = a.Source
		e.SourceURL = a.SourceURL
		e.Attribution = a.Attribution
		resolved = append(resolved, a)
		logger.Info("visual.asset.resolved", "index", i, "keyword", e.Keyword, "source", a.Source, "path", a.Path, "source_url", a.SourceURL, "license", a.License)
	}
	return out, resolved
}

func (r *Resolver) Resolve(ctx context.Context, keyword, query string) (Asset, error) {
	if err := os.MkdirAll(r.CacheDir, 0755); err != nil {
		return Asset{}, err
	}
	if a, ok := r.local(keyword, query); ok {
		return a, nil
	}
	if a, ok := r.cached(query); ok {
		return a, nil
	}
	if !r.Remote {
		return Asset{}, fmt.Errorf("nenhum asset local/cache e busca remota desativada")
	}
	return r.commons(ctx, keyword, query)
}

func (r *Resolver) local(keyword, query string) (Asset, bool) {
	b, err := os.ReadFile(r.Manifest)
	if err != nil {
		return Asset{}, false
	}
	var m manifest
	if json.Unmarshal(b, &m) != nil {
		return Asset{}, false
	}
	wanted := normalize(keyword)
	q := normalize(query)
	for _, x := range m.Assets {
		keys := append([]string{x.Keyword}, x.Aliases...)
		matched := false
		for _, k := range keys {
			nk := normalize(k)
			if nk != "" && (nk == wanted || nk == q || strings.Contains(q, nk)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		p := x.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.AssetDir, p)
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return Asset{Keyword: keyword, Query: query, Path: p, Source: "local"}, true
		}
	}
	return Asset{}, false
}

func (r *Resolver) cacheBase(query string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query))))
	return filepath.Join(r.CacheDir, hex.EncodeToString(h[:8]))
}

func (r *Resolver) cached(query string) (Asset, bool) {
	base := r.cacheBase(query)
	mb, err := os.ReadFile(base + ".json")
	if err != nil {
		return Asset{}, false
	}
	var a Asset
	if json.Unmarshal(mb, &a) != nil || a.Path == "" {
		return Asset{}, false
	}
	if _, err := os.Stat(a.Path); err != nil {
		return Asset{}, false
	}
	a.Source = "cache:" + strings.TrimPrefix(a.Source, "cache:")
	return a, true
}

type commonsResp struct {
	Query struct {
		Pages []struct {
			PageID    int    `json:"pageid"`
			Title     string `json:"title"`
			ImageInfo []struct {
				URL      string `json:"url"`
				ThumbURL string `json:"thumburl"`
				Mime     string `json:"mime"`
				Ext      map[string]struct {
					Value string `json:"value"`
				} `json:"extmetadata"`
			} `json:"imageinfo"`
		} `json:"pages"`
	} `json:"query"`
}

func (r *Resolver) commons(ctx context.Context, keyword, query string) (Asset, error) {
	u, _ := url.Parse(r.API)
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("generator", "search")
	q.Set("gsrsearch", query)
	q.Set("gsrnamespace", "6")
	q.Set("gsrlimit", "12")
	q.Set("prop", "imageinfo")
	q.Set("iiprop", "url|mime|extmetadata")
	q.Set("iiurlwidth", "1280")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Asset{}, err
	}
	req.Header.Set("User-Agent", "CENARIUS-AutoCut/1.4 (local video editor)")
	resp, err := r.Client.Do(req)
	if err != nil {
		return Asset{}, fmt.Errorf("Wikimedia Commons: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Asset{}, fmt.Errorf("Wikimedia Commons HTTP %s", resp.Status)
	}
	var cr commonsResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&cr); err != nil {
		return Asset{}, err
	}

	// formatversion=2 returns pages in search relevance order, so keep that order.
	for _, p := range cr.Query.Pages {
		if len(p.ImageInfo) == 0 {
			continue
		}
		title := p.Title
		ii := p.ImageInfo[0]
		if !supportedMime(ii.Mime) {
			continue
		}
		mediaURL := ii.ThumbURL
		if mediaURL == "" {
			mediaURL = ii.URL
		}
		if mediaURL == "" {
			continue
		}
		a, err := r.download(ctx, keyword, query, title, mediaURL, ii.Mime, ii.Ext)
		if err == nil {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("nenhuma imagem raster utilizável encontrada no Wikimedia Commons")
}

func (r *Resolver) download(ctx context.Context, keyword, query, title, mediaURL, mimeType string, ext map[string]struct {
	Value string `json:"value"`
}) (Asset, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	req.Header.Set("User-Agent", "CENARIUS-AutoCut/1.4")
	resp, err := r.Client.Do(req)
	if err != nil {
		return Asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Asset{}, fmt.Errorf("download asset HTTP %s", resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = mimeType
	}
	extension := extForMime(ct)
	if extension == "" {
		extension = extForMime(mimeType)
	}
	if extension == "" {
		return Asset{}, fmt.Errorf("tipo de imagem não suportado: %s", ct)
	}
	base := r.cacheBase(query)
	path := base + extension
	f, err := os.Create(path)
	if err != nil {
		return Asset{}, err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 20<<20))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return Asset{}, copyErr
	}
	if closeErr != nil {
		return Asset{}, closeErr
	}

	sourcePage := "https://commons.wikimedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	license := meta(ext, "LicenseShortName")
	author := stripHTML(meta(ext, "Artist"))
	credit := stripHTML(meta(ext, "Credit"))
	attribution := strings.TrimSpace(strings.Join(nonEmpty(author, license, credit), " · "))
	a := Asset{Keyword: keyword, Query: query, Path: path, Source: "wikimedia-commons", SourceURL: sourcePage, License: license, Author: author, Attribution: attribution}
	mb, _ := json.MarshalIndent(a, "", "  ")
	_ = os.WriteFile(base+".json", mb, 0644)
	return a, nil
}

func WriteAttributions(path string, xs []Asset) error {
	if xs == nil {
		xs = []Asset{}
	}
	b, _ := json.MarshalIndent(xs, "", "  ")
	return os.WriteFile(path, b, 0644)
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
func supportedMime(s string) bool {
	s = strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0]))
	return s == "image/jpeg" || s == "image/png" || s == "image/webp"
}
func extForMime(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0]))
	switch s {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	}
	if exts, _ := mime.ExtensionsByType(s); len(exts) > 0 {
		for _, e := range exts {
			if e == ".jpg" || e == ".jpeg" || e == ".png" || e == ".webp" {
				return e
			}
		}
	}
	return ""
}
func meta(m map[string]struct {
	Value string `json:"value"`
}, k string) string {
	if v, ok := m[k]; ok {
		return strings.TrimSpace(v.Value)
	}
	return ""
}
func stripHTML(s string) string {
	// Commons metadata often contains a small amount of HTML. A complete HTML
	// renderer is unnecessary for a machine-readable attribution sidecar.
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func nonEmpty(xs ...string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}
