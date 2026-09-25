package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
)

// Re-exported source names (see planner constants).
const (
	SourceLocal     = planner.SourceLocal
	SourceCache     = planner.SourceCache
	SourceCommons   = planner.SourceCommons
	SourceProcedure = planner.SourceProcedure
	SourceSelfBroll = planner.SourceSelfBroll
)

const maxRemoteAssetBytes int64 = 60 << 20

// Asset is one entry of assets-attribution.json.
type Asset struct {
	EventID     string  `json:"event_id,omitempty"`
	Keyword     string  `json:"keyword"`
	Query       string  `json:"query,omitempty"`
	Path        string  `json:"path"`
	Source      string  `json:"source"`
	Origin      string  `json:"origin,omitempty"`
	MediaType   string  `json:"media_type,omitempty"` // image | video
	URL         string  `json:"url,omitempty"`        // direct media URL
	SourceURL   string  `json:"source_url,omitempty"` // human page (Commons file page)
	License     string  `json:"license,omitempty"`
	Author      string  `json:"author,omitempty"`
	Attribution string  `json:"attribution,omitempty"`
	Duration    float64 `json:"duration,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
}

type manifestAsset struct {
	Keyword   string   `json:"keyword"`
	Aliases   []string `json:"aliases"`
	Concepts  []string `json:"concepts"`
	File      string   `json:"file"`
	License   string   `json:"license"`
	Author    string   `json:"author"`
	SourceURL string   `json:"source_url"`
}

type manifest struct {
	Assets []manifestAsset `json:"assets"`
}

// Resolver finds real media for visual events. Priority:
//
//  1. local asset library (assets/manifest.json)
//  2. cache (data/asset-cache)
//  3. real video from a free source (Wikimedia Commons)
//  4. real image from a free source
//  5. procedural motion graphic (FFmpeg)
//  6. self-broll — last fallback only, never counted as success
type Resolver struct {
	AssetDir       string
	Manifest       string
	CacheDir       string
	API            string
	Client         *http.Client
	Remote         bool
	Procedural     bool
	AllowSelfBroll bool
	FFmpeg         string
	MaxBytes       int64
	Validator      Validator
	// MaxSearches bounds remote API calls per event (queries x kinds).
	MaxSearches int
	// MaxCandidates bounds download attempts per search.
	MaxCandidates int

	cache Cache
}

// New keeps the v1.5 constructor signature (used by tests and tools).
func New(assetDir, manifestPath, cacheDir string) *Resolver {
	r := &Resolver{
		AssetDir: assetDir, Manifest: manifestPath, CacheDir: cacheDir,
		API: defaultCommonsAPI, Client: &http.Client{Timeout: 25 * time.Second},
		Remote:     os.Getenv("CENARIUS_NO_REMOTE_ASSETS") != "1",
		Procedural: true, AllowSelfBroll: true,
		FFmpeg: "ffmpeg", MaxBytes: maxRemoteAssetBytes,
		Validator:   FFValidator{FFprobe: "ffprobe", FFmpeg: "ffmpeg"},
		MaxSearches: 8, MaxCandidates: 5,
	}
	r.cache = Cache{Dir: cacheDir, NegTTL: 24 * time.Hour}
	return r
}

// NewFromConfig wires the resolver from the job configuration.
func NewFromConfig(cfg config.Config) *Resolver {
	r := New(cfg.Broll.AssetDir, cfg.Broll.Manifest, filepath.Join(cfg.WorkDir, "asset-cache"))
	r.Remote = r.Remote && cfg.Broll.Remote
	r.Procedural = cfg.Broll.Procedural
	r.AllowSelfBroll = cfg.Broll.AllowSelfBroll
	r.FFmpeg = cfg.FFmpeg
	r.Validator = FFValidator{FFprobe: cfg.FFprobe, FFmpeg: cfg.FFmpeg}
	if cfg.Broll.MaxRemoteMB > 0 {
		r.MaxBytes = int64(cfg.Broll.MaxRemoteMB) << 20
	}
	return r
}

func (r *Resolver) init() {
	if r.cache.Dir == "" {
		r.cache = Cache{Dir: r.CacheDir, NegTTL: 24 * time.Hour}
	}
	if r.Validator == nil {
		r.Validator = FFValidator{}
	}
	if r.MaxBytes <= 0 {
		r.MaxBytes = maxRemoteAssetBytes
	}
	if r.MaxSearches <= 0 {
		r.MaxSearches = 8
	}
	if r.MaxCandidates <= 0 {
		r.MaxCandidates = 5
	}
	if r.Client == nil {
		r.Client = &http.Client{Timeout: 25 * time.Second}
	}
	if r.API == "" {
		r.API = defaultCommonsAPI
	}
}

// ResolvePlan attaches an asset to every visual event and returns the
// attribution list. Events that cannot get any asset (self-broll disabled)
// are removed from the plan.
func (r *Resolver) ResolvePlan(ctx context.Context, p planner.Plan) (planner.Plan, []Asset) {
	r.init()
	logger := logx.From(ctx)
	if err := r.cache.Ensure(); err != nil {
		logger.Warn("visual.cache.mkdir_failed", "dir", r.CacheDir, "error", err)
	}
	planner.MigrateLegacy(&p)
	out := p
	out.VisualEvents = nil
	var attributions []Asset
	counts := map[string]int{}
	for _, e := range p.VisualEvents {
		started := time.Now()
		ref, a, err := r.ResolveEvent(ctx, e)
		if err != nil {
			logger.Warn("visual.asset.fallback", "event", e.ID, "concept", e.Concept, "fallback", "drop", "error", err)
			counts["dropped"]++
			continue
		}
		e.Asset = ref
		counts[ref.Source]++
		if a != nil {
			a.EventID = e.ID
			attributions = append(attributions, *a)
		}
		logger.Info("visual.asset.event_done", "event", e.ID, "source", ref.Source, "type", ref.Type, "fallback", ref.Fallback, "duration_ms", time.Since(started).Milliseconds())
		out.VisualEvents = append(out.VisualEvents, e)
	}
	total := len(p.VisualEvents)
	logger.Info("visual.asset.summary", "events", total, "local", counts[SourceLocal], "cache", counts[SourceCache],
		"remote", counts[SourceCommons], "procedural", counts[SourceProcedure], "self_broll", counts[SourceSelfBroll], "dropped", counts["dropped"])
	if total > 0 && counts[SourceSelfBroll]*2 >= total && counts[SourceSelfBroll] > 0 {
		logger.Warn("visual.asset.self_broll_majority", "self_broll", counts[SourceSelfBroll], "events", total,
			"hint", "nenhum B-roll real foi encontrado: adicione assets locais em assets/manifest.json ou verifique a rede/Wikimedia Commons")
	}
	return out, attributions
}

func (r *Resolver) kindsFor(e planner.VisualEvent) []string {
	if e.Layout == planner.LayoutCard || e.Layout == planner.LayoutPIP || e.Type == planner.TypeCard {
		return []string{"image", "video"}
	}
	return []string{"video", "image"}
}

func eventQueries(e planner.VisualEvent) []string {
	var qs []string
	seen := map[string]bool{}
	for _, q := range append(append([]string(nil), e.Queries...), e.Concept) {
		q = strings.TrimSpace(q)
		if q != "" && !seen[normQuery(q)] {
			seen[normQuery(q)] = true
			qs = append(qs, q)
		}
	}
	return qs
}

// ResolveEvent walks the priority chain for one event.
func (r *Resolver) ResolveEvent(ctx context.Context, e planner.VisualEvent) (*planner.AssetRef, *Asset, error) {
	r.init()
	logger := logx.From(ctx)
	if err := r.cache.Ensure(); err != nil {
		logger.Warn("visual.cache.mkdir_failed", "dir", r.CacheDir, "error", err)
	}
	qs := eventQueries(e)
	kinds := r.kindsFor(e)

	// 1. local library
	for _, q := range append(qs, e.Label) {
		if a, ok := r.local(ctx, e.Concept, q); ok {
			return r.done(ctx, e, a, "local")
		}
	}
	// 2. cache
	for _, kind := range kinds {
		for _, q := range qs {
			if a, ok := r.cache.Lookup(kind, q); ok {
				logger.Info("visual.asset.cache_hit", "event", e.ID, "level", "query", "query", q, "kind", kind, "path", a.Path)
				a.Origin = strings.TrimPrefix(a.Source, "cache:")
				if a.Origin == SourceCache || a.Origin == "" {
					a.Origin = SourceCommons
				}
				a.Source = SourceCache
				return r.done(ctx, e, a, "cache")
			}
		}
	}
	// 3+4. remote video, then remote image (image first for cards)
	var lastErr error
	if r.Remote {
		searches := 0
		for _, kind := range kinds {
			for _, q := range qs {
				if searches >= r.MaxSearches {
					break
				}
				if r.cache.RecentMiss(kind, q) {
					logger.Debug("visual.asset.query", "event", e.ID, "query", q, "kind", kind, "skip", "recent miss")
					continue
				}
				searches++
				logger.Info("visual.asset.query", "event", e.ID, "concept", e.Concept, "query", q, "kind", kind, "attempt", searches)
				a, err := r.remote(ctx, e, q, kind)
				if err == nil {
					r.cache.Store(kind, q, a)
					return r.done(ctx, e, a, "remote-"+kind)
				}
				lastErr = err
				logger.Info("visual.asset.query_miss", "event", e.ID, "query", q, "kind", kind, "error", err)
				if !isTransient(err) {
					r.cache.MarkMiss(kind, q)
				}
			}
		}
	} else {
		logger.Info("visual.asset.query", "event", e.ID, "remote", false, "reason", "busca remota desativada")
	}
	// 5. procedural motion graphic
	label := strings.TrimSpace(e.Label)
	if label == "" {
		label = strings.ToUpper(strings.TrimSpace(e.Concept))
	}
	if r.Procedural && label != "" {
		path := r.cache.ProceduralPath(label, 1080, 1920)
		if _, err := os.Stat(path); err == nil {
			logger.Info("visual.asset.cache_hit", "event", e.ID, "level", "procedural", "path", path)
		} else if err := GenerateMotion(ctx, r.FFmpeg, label, path, 1080, 1920, 4.0); err != nil {
			logger.Warn("visual.asset.invalid", "event", e.ID, "source", SourceProcedure, "error", err)
			path = ""
		}
		if path != "" {
			if m, err := r.Validator.Validate(ctx, path, "video"); err == nil {
				logger.Info("visual.asset.fallback", "event", e.ID, "concept", e.Concept, "fallback", SourceProcedure, "label", label, "reason", errString(lastErr))
				a := Asset{Keyword: e.Concept, Query: label, Path: path, Source: SourceProcedure, MediaType: "video", License: "generated locally (CENARIUS)", Duration: m.Duration, Width: m.Width, Height: m.Height}
				ref := toRef(a)
				ref.Fallback = true
				return ref, &a, nil
			} else {
				logger.Warn("visual.asset.invalid", "event", e.ID, "source", SourceProcedure, "error", err)
			}
		}
	}
	// 6. self-broll (last resort)
	if r.AllowSelfBroll {
		logger.Warn("visual.asset.fallback", "event", e.ID, "concept", e.Concept, "fallback", SourceSelfBroll, "reason", errString(lastErr))
		return &planner.AssetRef{Source: SourceSelfBroll, Type: "video", Fallback: true}, nil, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("nenhum asset para %q", e.Concept)
	}
	return nil, nil, lastErr
}

// Resolve keeps the v1.5 API: local -> cache -> remote, no fallbacks.
func (r *Resolver) Resolve(ctx context.Context, keyword, query string) (Asset, error) {
	r.init()
	saveP, saveS := r.Procedural, r.AllowSelfBroll
	r.Procedural, r.AllowSelfBroll = false, false
	defer func() { r.Procedural, r.AllowSelfBroll = saveP, saveS }()
	_ = r.cache.Ensure()
	ref, a, err := r.ResolveEvent(ctx, planner.VisualEvent{Concept: keyword, Queries: []string{query}, Layout: planner.LayoutReaction})
	if err != nil {
		return Asset{}, err
	}
	if a != nil {
		return *a, nil
	}
	return Asset{Keyword: keyword, Query: query, Path: ref.Path, Source: ref.Source, MediaType: ref.Type}, nil
}

func (r *Resolver) done(ctx context.Context, e planner.VisualEvent, a Asset, tier string) (*planner.AssetRef, *Asset, error) {
	logx.From(ctx).Info("visual.asset.resolved", "event", e.ID, "concept", e.Concept, "tier", tier, "source", a.Source, "origin", a.Origin,
		"media_type", a.MediaType, "query", a.Query, "path", a.Path, "url", a.URL, "license", a.License, "author", a.Author)
	a.Keyword = e.Concept
	return toRef(a), &a, nil
}

func toRef(a Asset) *planner.AssetRef {
	return &planner.AssetRef{Path: a.Path, Type: a.MediaType, Source: a.Source, Origin: a.Origin, Query: a.Query,
		URL: firstNonEmpty(a.SourceURL, a.URL), License: a.License, Author: a.Author, Duration: a.Duration}
}

func (r *Resolver) remote(ctx context.Context, e planner.VisualEvent, query, kind string) (Asset, error) {
	logger := logx.From(ctx)
	cands, err := r.searchCommons(ctx, query, kind, e.Layout)
	if err != nil {
		return Asset{}, transientErr{err}
	}
	if len(cands) == 0 {
		return Asset{}, fmt.Errorf("nenhum candidato %s para %q", kind, query)
	}
	tried := 0
	for _, c := range cands {
		if tried >= r.MaxCandidates {
			break
		}
		tried++
		for i, u := range c.URLs {
			if i >= 5 {
				break
			}
			path, m, err := r.download(ctx, u, kind)
			if err != nil {
				logger.Info("visual.asset.invalid", "event", e.ID, "title", c.Title, "url", u, "error", err)
				continue
			}
			logger.Info("visual.asset.valid", "event", e.ID, "title", c.Title, "path", path, "width", m.Width, "height", m.Height, "duration_s", m.Duration)
			attribution := strings.Join(nonEmpty(c.Author, c.License, c.Credit, "via Wikimedia Commons"), " · ")
			return Asset{Keyword: e.Concept, Query: query, Path: path, Source: SourceCommons, MediaType: kind, URL: u,
				SourceURL: c.Page, License: c.License, Author: c.Author, Attribution: attribution,
				Duration: m.Duration, Width: m.Width, Height: m.Height}, nil
		}
	}
	return Asset{}, fmt.Errorf("nenhum %s válido entre %d candidatos para %q", kind, tried, query)
}

type transientErr struct{ error }

func isTransient(err error) bool { _, ok := err.(transientErr); return ok }

func (r *Resolver) local(ctx context.Context, concept, query string) (Asset, bool) {
	b, err := os.ReadFile(r.Manifest)
	if err != nil {
		return Asset{}, false
	}
	var m manifest
	if json.Unmarshal(b, &m) != nil {
		return Asset{}, false
	}
	wanted := normalize(concept)
	q := normalize(query)
	qWords := " " + strings.Join(tokenize(q), " ") + " "
	for _, x := range m.Assets {
		keys := append(append([]string{x.Keyword}, x.Aliases...), x.Concepts...)
		matched := false
		for _, k := range keys {
			nk := normalize(k)
			if nk == "" {
				continue
			}
			if nk == wanted || nk == q || strings.Contains(qWords, " "+strings.Join(tokenize(nk), " ")+" ") {
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
		kind := mediaTypeForPath(p)
		md, err := r.Validator.Validate(ctx, p, kind)
		if err != nil {
			logx.From(ctx).Warn("visual.asset.invalid", "source", SourceLocal, "path", p, "error", err)
			continue
		}
		license := x.License
		if license == "" {
			license = "local (fornecido pelo usuário)"
		}
		return Asset{Keyword: concept, Query: query, Path: p, Source: SourceLocal, MediaType: kind, SourceURL: x.SourceURL,
			License: license, Author: x.Author, Duration: md.Duration, Width: md.Width, Height: md.Height}, true
	}
	return Asset{}, false
}

// WriteAttributions writes assets-attribution.json (source, URL, license,
// author, attribution) for every real or generated asset used in the edit.
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
	switch strings.ToLower(filepath.Ext(p)) {
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
			if e == ".jpg" || e == ".jpeg" || e == ".png" || e == ".webp" || e == ".webm" || e == ".mp4" || e == ".ogv" {
				return e
			}
		}
	}
	return ""
}

func meta(m extMeta, k string) string {
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

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func itoa(n int) string { return strconv.Itoa(n) }
