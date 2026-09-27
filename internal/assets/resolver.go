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
	SourcePexels    = planner.SourcePexels
	SourcePixabay   = planner.SourcePixabay
	SourceManual    = planner.SourceManual
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
//  3. Pexels video
//  4. Pixabay video
//  5. Pexels image
//  6. Pixabay image
//  7. Wikimedia Commons (video, then image)
//  8. procedural motion graphic (FFmpeg)
//  9. self-broll — last fallback only, never counted as success
type Resolver struct {
	AssetDir       string
	Manifest       string
	CacheDir       string
	API            string // Wikimedia Commons API (kept for backwards-compatible tests)
	PexelsAPI      string
	PixabayAPI     string
	PexelsKey      string
	PixabayKey     string
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
		API: defaultCommonsAPI, PexelsAPI: defaultPexelsAPI, PixabayAPI: defaultPixabayAPI,
		PexelsKey: strings.TrimSpace(os.Getenv("PEXELS_API_KEY")), PixabayKey: strings.TrimSpace(os.Getenv("PIXABAY_API_KEY")),
		Client:     &http.Client{Timeout: 25 * time.Second},
		Remote:     os.Getenv("CENARIUS_NO_REMOTE_ASSETS") != "1",
		Procedural: true, AllowSelfBroll: true,
		FFmpeg: "ffmpeg", MaxBytes: maxRemoteAssetBytes,
		Validator:   FFValidator{FFprobe: "ffprobe", FFmpeg: "ffmpeg"},
		MaxSearches: 18, MaxCandidates: 5,
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
		r.MaxSearches = 18
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
	if r.PexelsAPI == "" {
		r.PexelsAPI = defaultPexelsAPI
	}
	if r.PixabayAPI == "" {
		r.PixabayAPI = defaultPixabayAPI
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
	used := map[string]bool{}
	usedConcepts := map[string]int{}
	for _, e := range p.VisualEvents {
		started := time.Now()
		if e.Asset != nil && e.Asset.Source == planner.SourceManual && strings.TrimSpace(e.Asset.Path) != "" {
			m, err := r.Validator.Validate(ctx, e.Asset.Path, firstNonEmpty(e.Asset.Type, "video"))
			if err != nil {
				logger.Warn("visual.asset.manual_invalid", "event", e.ID, "path", e.Asset.Path, "error", err)
				counts["dropped"]++
				continue
			}
			if e.Asset.Duration <= 0 {
				e.Asset.Duration = m.Duration
			}
			counts[planner.SourceManual]++
			markUsedRef(used, e.Asset)
			usedConcepts[normalize(e.Concept)]++
			attributions = append(attributions, Asset{EventID: e.ID, Keyword: e.Concept, Path: e.Asset.Path, Source: planner.SourceManual, MediaType: e.Asset.Type, License: "user-provided", Attribution: "Arquivo fornecido pelo usuário", Duration: e.Asset.Duration, Width: m.Width, Height: m.Height})
			logger.Info("visual.asset.event_done", "event", e.ID, "source", planner.SourceManual, "type", e.Asset.Type, "duration_ms", time.Since(started).Milliseconds())
			out.VisualEvents = append(out.VisualEvents, e)
			continue
		}
		ref, a, err := r.resolveEvent(ctx, e, used)
		if err != nil {
			logger.Warn("visual.asset.fallback", "event", e.ID, "concept", e.Concept, "fallback", "drop", "error", err)
			counts["dropped"]++
			continue
		}
		e.Asset = ref
		counts[ref.Source]++
		markUsedRef(used, ref)
		usedConcepts[normalize(e.Concept)]++
		if a != nil {
			a.EventID = e.ID
			attributions = append(attributions, *a)
		}
		logger.Info("visual.asset.event_done", "event", e.ID, "source", ref.Source, "type", ref.Type, "fallback", ref.Fallback, "duration_ms", time.Since(started).Milliseconds())
		out.VisualEvents = append(out.VisualEvents, e)
	}
	total := len(p.VisualEvents)
	logger.Info("visual.asset.summary", "events", total, "local", counts[SourceLocal], "cache", counts[SourceCache],
		"pexels", counts[SourcePexels], "pixabay", counts[SourcePixabay], "manual", counts[SourceManual], "commons", counts[SourceCommons],
		"procedural", counts[SourceProcedure], "self_broll", counts[SourceSelfBroll], "dropped", counts["dropped"], "unique_concepts", len(usedConcepts))
	if total > 0 && counts[SourceSelfBroll]*2 >= total && counts[SourceSelfBroll] > 0 {
		logger.Warn("visual.asset.self_broll_majority", "self_broll", counts[SourceSelfBroll], "events", total,
			"hint", "nenhum B-roll real foi encontrado: adicione assets locais, configure PEXELS_API_KEY/PIXABAY_API_KEY ou verifique a rede/Wikimedia Commons")
	}
	return out, attributions
}

func (r *Resolver) kindsFor(e planner.VisualEvent) []string {
	// Prefer moving footage for every layout. Cards/PiP can still use video and
	// fall back to images when that is the better/only semantic match.
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

// ResolveEvent walks the priority chain for one event. It intentionally does
// not share a used-set so callers outside a render retain the v1.6 behaviour.
func (r *Resolver) ResolveEvent(ctx context.Context, e planner.VisualEvent) (*planner.AssetRef, *Asset, error) {
	return r.resolveEvent(ctx, e, nil)
}

// resolveEvent is the render-aware resolver. When used is non-nil, an asset
// already selected earlier in the same plan is skipped and the next candidate
// is tried. This prevents the visually cheap "same B-roll again" failure mode.
func (r *Resolver) resolveEvent(ctx context.Context, e planner.VisualEvent, used map[string]bool) (*planner.AssetRef, *Asset, error) {
	r.init()
	logger := logx.From(ctx)
	if err := r.cache.Ensure(); err != nil {
		logger.Warn("visual.cache.mkdir_failed", "dir", r.CacheDir, "error", err)
	}
	qs := eventQueries(e)
	kinds := r.kindsFor(e)

	// 1. local library
	for _, q := range append(qs, e.Label) {
		if a, ok := r.local(ctx, e.Concept, q, used); ok {
			return r.done(ctx, e, a, "local")
		}
	}
	// 2. cache
	for _, kind := range kinds {
		for _, q := range qs {
			if a, ok := r.cache.Lookup(kind, q); ok {
				if assetUsed(used, a) {
					logger.Info("visual.asset.duplicate_skip", "event", e.ID, "tier", "cache", "query", q, "path", a.Path)
					continue
				}
				logger.Info("visual.asset.cache_hit", "event", e.ID, "level", "query", "query", q, "kind", kind, "path", a.Path)
				origin := a.Origin
				if origin == "" || origin == SourceCache {
					origin = a.Source
				}
				if origin == "" || origin == SourceCache {
					origin = SourceCommons
				}
				a.Origin = origin
				a.Source = SourceCache
				return r.done(ctx, e, a, "cache")
			}
		}
	}
	// 3..7. remote providers in editorial priority order:
	// Pexels video -> Pixabay video -> Pexels image -> Pixabay image -> Commons.
	var lastErr error
	if r.Remote {
		type tier struct{ provider, kind string }
		tiers := []tier{}
		if r.PexelsKey != "" {
			tiers = append(tiers, tier{SourcePexels, "video"})
		}
		if r.PixabayKey != "" {
			tiers = append(tiers, tier{SourcePixabay, "video"})
		}
		if r.PexelsKey != "" {
			tiers = append(tiers, tier{SourcePexels, "image"})
		}
		if r.PixabayKey != "" {
			tiers = append(tiers, tier{SourcePixabay, "image"})
		}
		tiers = append(tiers, tier{SourceCommons, "video"}, tier{SourceCommons, "image"})
		searches := 0
		for _, t := range tiers {
			for qi, q := range qs {
				// Three semantically distinct queries per provider tier are enough to
				// explore alternatives while guaranteeing the lower-priority providers
				// are still reached before the per-event request budget is exhausted.
				if qi >= 3 {
					break
				}
				if searches >= r.MaxSearches {
					break
				}
				missKey := t.provider + ":" + t.kind
				if r.cache.RecentMiss(missKey, q) {
					logger.Debug("visual.asset.query", "event", e.ID, "provider", t.provider, "query", q, "kind", t.kind, "skip", "recent miss")
					continue
				}
				searches++
				logger.Info("visual.asset.query", "event", e.ID, "concept", e.Concept, "provider", t.provider, "query", q, "kind", t.kind, "attempt", searches)
				a, err := r.remoteProvider(ctx, e, q, t.kind, t.provider, used)
				if err == nil {
					r.cache.Store(t.kind, q, a)
					return r.done(ctx, e, a, "remote-"+t.provider+"-"+t.kind)
				}
				lastErr = err
				logger.Info("visual.asset.query_miss", "event", e.ID, "provider", t.provider, "query", q, "kind", t.kind, "error", err)
				if !isTransient(err) {
					r.cache.MarkMiss(missKey, q)
				}
			}
			if searches >= r.MaxSearches {
				break
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

func (r *Resolver) remoteProvider(ctx context.Context, e planner.VisualEvent, query, kind, provider string, used map[string]bool) (Asset, error) {
	logger := logx.From(ctx)
	var (
		cands []Candidate
		err   error
	)
	switch provider {
	case SourcePexels:
		cands, err = r.searchPexels(ctx, query, kind, e.Layout)
	case SourcePixabay:
		cands, err = r.searchPixabay(ctx, query, kind, e.Layout)
	case SourceCommons:
		cands, err = r.searchCommons(ctx, query, kind, e.Layout)
	default:
		return Asset{}, fmt.Errorf("provedor desconhecido: %s", provider)
	}
	if err != nil {
		return Asset{}, transientErr{err}
	}
	if len(cands) == 0 {
		return Asset{}, fmt.Errorf("%s: nenhum candidato %s para %q", provider, kind, query)
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
			if used != nil && used["url:"+u] {
				logger.Info("visual.asset.duplicate_skip", "event", e.ID, "provider", provider, "title", c.Title, "url", u)
				continue
			}
			path, m, err := r.download(ctx, provider, u, kind)
			if err != nil {
				logger.Info("visual.asset.invalid", "event", e.ID, "provider", provider, "title", c.Title, "url", u, "error", err)
				continue
			}
			a := Asset{Keyword: e.Concept, Query: query, Path: path, Source: provider, MediaType: kind, URL: u,
				SourceURL: c.Page, License: c.License, Author: c.Author,
				Attribution: strings.Join(nonEmpty(c.Author, c.License, c.Credit, "via "+providerLabel(provider)), " · "),
				Duration:    m.Duration, Width: m.Width, Height: m.Height}
			if assetUsed(used, a) {
				logger.Info("visual.asset.duplicate_skip", "event", e.ID, "provider", provider, "title", c.Title, "path", path)
				continue
			}
			logger.Info("visual.asset.valid", "event", e.ID, "provider", provider, "title", c.Title, "path", path, "width", m.Width, "height", m.Height, "duration_s", m.Duration)
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("%s: nenhum %s válido entre %d candidatos para %q", provider, kind, tried, query)
}

func providerLabel(source string) string {
	switch source {
	case SourcePexels:
		return "Pexels"
	case SourcePixabay:
		return "Pixabay"
	default:
		return "Wikimedia Commons"
	}
}

func assetUsed(used map[string]bool, a Asset) bool {
	if used == nil {
		return false
	}
	for _, k := range assetKeys(a.Path, a.URL, a.SourceURL) {
		if used[k] {
			return true
		}
	}
	return false
}

func markUsedRef(used map[string]bool, ref *planner.AssetRef) {
	if used == nil || ref == nil || ref.Source == SourceSelfBroll || ref.Source == SourceProcedure {
		return
	}
	for _, k := range assetKeys(ref.Path, ref.URL) {
		used[k] = true
	}
}

func assetKeys(values ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		prefix := "url:"
		if strings.HasPrefix(v, "/") || strings.HasPrefix(v, ".") {
			prefix = "path:"
		}
		k := prefix + v
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

type transientErr struct{ error }

func isTransient(err error) bool { _, ok := err.(transientErr); return ok }

func (r *Resolver) local(ctx context.Context, concept, query string, used map[string]bool) (Asset, bool) {
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
		a := Asset{Keyword: concept, Query: query, Path: p, Source: SourceLocal, MediaType: kind, SourceURL: x.SourceURL,
			License: license, Author: x.Author, Duration: md.Duration, Width: md.Width, Height: md.Height}
		if assetUsed(used, a) {
			logx.From(ctx).Info("visual.asset.duplicate_skip", "tier", "local", "query", query, "path", p)
			continue
		}
		return a, true
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
