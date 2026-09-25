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
const maxRemoteAssetBytes int64 = 60 << 20 // 60 MiB keeps local edits responsive.

type Asset struct {
	Keyword     string `json:"keyword"`
	Query       string `json:"query,omitempty"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	MediaType   string `json:"media_type,omitempty"` // image | video
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
		Client:   &http.Client{Timeout: 25 * time.Second},
		Remote:   remote,
	}
}

// ResolvePlan resolves each planned visual event to a local image/video.
// Order: local manifest -> cache -> Wikimedia Commons video -> Commons image.
// No paid API is required. If nothing resolves, the renderer uses an animated
// self-B-roll fallback rather than a static black card.
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
			e.AssetSource = "self-broll"
			e.AssetType = "video"
			continue
		}
		e.Asset = a.Path
		e.AssetSource = a.Source
		e.AssetType = a.MediaType
		e.SourceURL = a.SourceURL
		e.Attribution = a.Attribution
		resolved = append(resolved, a)
		logger.Info("visual.asset.resolved", "index", i, "keyword", e.Keyword, "source", a.Source, "media_type", a.MediaType, "path", a.Path, "source_url", a.SourceURL, "license", a.License)
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
			return Asset{Keyword: keyword, Query: query, Path: p, Source: "local", MediaType: mediaTypeForPath(p)}, true
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
	if a.MediaType == "" {
		a.MediaType = mediaTypeForPath(a.Path)
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
				Size     int64  `json:"size"`
				Ext      map[string]struct {
					Value string `json:"value"`
				} `json:"extmetadata"`
			} `json:"imageinfo"`
		} `json:"pages"`
	} `json:"query"`
}

// commons searches moving footage first. This is what turns the visual layer
// into real B-roll instead of a static illustration. If no usable video is
// available it falls back to a raster image.
func (r *Resolver) commons(ctx context.Context, keyword, query string) (Asset, error) {
	searches := []struct {
		Q         string
		WantVideo bool
	}{
		{Q: query + " filetype:video", WantVideo: true},
		{Q: query, WantVideo: false},
	}
	var lastErr error
	for _, s := range searches {
		a, err := r.commonsSearch(ctx, keyword, query, s.Q, s.WantVideo)
		if err == nil {
			return a, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("nenhum B-roll utilizável encontrado")
	}
	return Asset{}, lastErr
}

func (r *Resolver) commonsSearch(ctx context.Context, keyword, cacheQuery, searchQuery string, preferVideo bool) (Asset, error) {
	u, _ := url.Parse(r.API)
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("generator", "search")
	q.Set("gsrsearch", searchQuery)
	q.Set("gsrnamespace", "6")
	q.Set("gsrlimit", "24")
	q.Set("prop", "imageinfo")
	q.Set("iiprop", "url|mime|size|extmetadata")
	q.Set("iiurlwidth", "1280")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Asset{}, err
	}
	req.Header.Set("User-Agent", "CENARIUS-AutoCut/1.5 (local video editor)")
	resp, err := r.Client.Do(req)
	if err != nil {
		return Asset{}, fmt.Errorf("Wikimedia Commons: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Asset{}, fmt.Errorf("Wikimedia Commons HTTP %s", resp.Status)
	}
	var cr commonsResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&cr); err != nil {
		return Asset{}, err
	}

	// Pass 1 picks the preferred media kind, pass 2 accepts the other kind.
	for pass := 0; pass < 2; pass++ {
		for _, p := range cr.Query.Pages {
			if len(p.ImageInfo) == 0 {
				continue
			}
			ii := p.ImageInfo[0]
			isVideo := isVideoMime(ii.Mime)
			if pass == 0 && isVideo != preferVideo {
				continue
			}
			if pass == 1 && isVideo == preferVideo {
				continue
			}
			if !supportedMime(ii.Mime) {
				continue
			}
			if ii.Size > maxRemoteAssetBytes {
				continue
			}
			mediaURL := ii.URL
			if !isVideo && ii.ThumbURL != "" {
				mediaURL = ii.ThumbURL
			}
			if mediaURL == "" {
				continue
			}
			a, err := r.download(ctx, keyword, cacheQuery, p.Title, mediaURL, ii.Mime, ii.Ext)
			if err == nil {
				return a, nil
			}
		}
	}
	return Asset{}, fmt.Errorf("nenhum vídeo/imagem utilizável encontrado no Wikimedia Commons para %q", searchQuery)
}

func (r *Resolver) download(ctx context.Context, keyword, query, title, mediaURL, mimeType string, ext map[string]struct {
	Value string `json:"value"`
}) (Asset, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	req.Header.Set("User-Agent", "CENARIUS-AutoCut/1.5")
	resp, err := r.Client.Do(req)
	if err != nil {
		return Asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Asset{}, fmt.Errorf("download asset HTTP %s", resp.Status)
	}
	if resp.ContentLength > maxRemoteAssetBytes {
		return Asset{}, fmt.Errorf("asset remoto grande demais: %d bytes", resp.ContentLength)
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
		return Asset{}, fmt.Errorf("tipo de mídia não suportado: %s", ct)
	}
	base := r.cacheBase(query)
	path := base + extension
	f, err := os.Create(path)
	if err != nil {
		return Asset{}, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxRemoteAssetBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return Asset{}, copyErr
	}
	if n > maxRemoteAssetBytes {
		_ = os.Remove(path)
		return Asset{}, fmt.Errorf("asset remoto excedeu limite de 60 MiB")
	}
	if closeErr != nil {
		return Asset{}, closeErr
	}

	sourcePage := "https://commons.wikimedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	license := meta(ext, "LicenseShortName")
	author := stripHTML(meta(ext, "Artist"))
	credit := stripHTML(meta(ext, "Credit"))
	attribution := strings.TrimSpace(strings.Join(nonEmpty(author, license, credit), " · "))
	a := Asset{Keyword: keyword, Query: query, Path: path, Source: "wikimedia-commons", MediaType: mediaTypeForPath(path), SourceURL: sourcePage, License: license, Author: author, Attribution: attribution}
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

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func supportedMime(s string) bool {
	s = strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0]))
	return s == "image/jpeg" || s == "image/png" || s == "image/webp" ||
		s == "video/webm" || s == "video/mp4" || s == "video/ogg" || s == "application/ogg"
}

func isVideoMime(s string) bool {
	s = strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0]))
	return strings.HasPrefix(s, "video/") || s == "application/ogg"
}

func mediaTypeForPath(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".webm", ".mp4", ".m4v", ".mov", ".ogv", ".ogg", ".mpeg", ".mpg":
		return "video"
	default:
		return "image"
	}
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
	case "video/webm":
		return ".webm"
	case "video/mp4":
		return ".mp4"
	case "video/ogg", "application/ogg":
		return ".ogv"
	}
	if exts, _ := mime.ExtensionsByType(s); len(exts) > 0 {
		for _, e := range exts {
			e = strings.ToLower(e)
			if e == ".jpg" || e == ".jpeg" || e == ".png" || e == ".webp" || e == ".webm" || e == ".mp4" || e == ".ogv" || e == ".ogg" {
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
