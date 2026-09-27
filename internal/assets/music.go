package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/logx"
	"cenarius-autocut/internal/planner"
)

// Music sources.
const (
	SourceMusicLocal  = "music-local"
	SourceMusicFile   = "music-file"
	SourceOpenverse   = "openverse"
	defaultOpenverse  = "https://api.openverse.org/v1"
	minMusicDurationS = 20.0
)

type musicTrack struct {
	File      string   `json:"file"`
	Title     string   `json:"title"`
	Moods     []string `json:"moods"`
	License   string   `json:"license"`
	Author    string   `json:"author"`
	SourceURL string   `json:"source_url"`
}

type musicManifest struct {
	Tracks []musicTrack `json:"tracks"`
}

// moodGenres helps judge Openverse results that carry genre/tag metadata.
var moodGenres = map[string][]string{
	planner.MoodEnergetic: {"electronic", "pop", "dance", "upbeat", "energetic", "corporate", "house", "edm", "rock"},
	planner.MoodInspiring: {"inspiring", "uplifting", "piano", "cinematic", "motivational", "ambient", "emotional"},
	planner.MoodTense:     {"suspense", "tension", "dark", "thriller", "horror", "ambient", "drone"},
	planner.MoodDramatic:  {"cinematic", "dramatic", "epic", "orchestral", "trailer", "soundtrack"},
	planner.MoodChill:     {"lofi", "chill", "hiphop", "acoustic", "calm", "jazz", "relax"},
	planner.MoodFunny:     {"funny", "quirky", "comedy", "ukulele", "playful", "cartoon", "happy"},
}

// ResolveMusic finds a licence-safe track for the cue. Priority:
//
//  1. cfg.File (explicit track, e.g. CLI -music)
//  2. local library manifest filtered by mood (tracks you own or that the
//     YouTube Audio Library / other libraries licence for use in videos)
//  3. cache of a previous Openverse pick for the same mood query
//  4. Openverse audio (CC0 / CC BY / CC BY-SA music, no API key)
//
// No track is not an error: the reel still gets the emotional SFX.
func (r *Resolver) ResolveMusic(ctx context.Context, cue *planner.MusicCue, cfg config.MusicConfig) (*planner.AssetRef, *Asset, error) {
	r.init()
	logger := logx.From(ctx)
	if cue == nil {
		return nil, nil, fmt.Errorf("sem cue de música")
	}
	if err := r.cache.Ensure(); err != nil {
		logger.Warn("audio.music.cache_mkdir_failed", "dir", r.CacheDir, "error", err)
	}
	started := time.Now()
	if f := strings.TrimSpace(cfg.File); f != "" {
		m, err := r.Validator.Validate(ctx, f, "audio")
		if err != nil {
			return nil, nil, fmt.Errorf("trilha informada inválida (%s): %w", f, err)
		}
		a := Asset{EventID: "music", Keyword: "music:" + cue.Mood, Path: f, Source: SourceMusicFile, MediaType: "audio",
			License: "fornecido pelo usuário", Duration: m.Duration}
		logger.Info("audio.music.resolved", "tier", "file", "mood", cue.Mood, "path", f, "duration_s", m.Duration)
		return musicRef(a), &a, nil
	}
	if a, ok := r.localMusic(ctx, cue.Mood, cfg); ok {
		logger.Info("audio.music.resolved", "tier", "local", "mood", cue.Mood, "path", a.Path, "license", a.License, "duration_ms", time.Since(started).Milliseconds())
		return musicRef(a), &a, nil
	}
	for _, q := range cue.Queries {
		if a, ok := r.cache.Lookup("audio", q); ok {
			logger.Info("audio.music.cache_hit", "mood", cue.Mood, "query", q, "path", a.Path)
			a.Origin, a.Source = firstNonEmpty(a.Origin, a.Source), SourceCache
			return musicRef(a), &a, nil
		}
	}
	var lastErr error
	if r.Remote && cfg.Remote {
		for _, q := range cue.Queries {
			if r.cache.RecentMiss(SourceOpenverse+":audio", q) {
				continue
			}
			a, err := r.openverseMusic(ctx, cue.Mood, q)
			if err == nil {
				r.cache.Store("audio", q, a)
				logger.Info("audio.music.resolved", "tier", "openverse", "mood", cue.Mood, "query", q, "title", a.Keyword,
					"license", a.License, "author", a.Author, "duration_s", a.Duration, "duration_ms", time.Since(started).Milliseconds())
				a.EventID = "music"
				return musicRef(a), &a, nil
			}
			lastErr = err
			logger.Info("audio.music.query_miss", "provider", SourceOpenverse, "query", q, "error", err)
			if !isTransient(err) {
				r.cache.MarkMiss(SourceOpenverse+":audio", q)
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("nenhuma trilha local para o mood %q e busca remota desativada", cue.Mood)
	}
	return nil, nil, lastErr
}

func musicRef(a Asset) *planner.AssetRef {
	ref := toRef(a)
	ref.Type = "audio"
	return ref
}

func (r *Resolver) localMusic(ctx context.Context, mood string, cfg config.MusicConfig) (Asset, bool) {
	b, err := os.ReadFile(cfg.Manifest)
	if err != nil {
		return Asset{}, false
	}
	var m musicManifest
	if json.Unmarshal(b, &m) != nil {
		logx.From(ctx).Warn("audio.music.manifest_invalid", "path", cfg.Manifest)
		return Asset{}, false
	}
	for _, t := range m.Tracks {
		match := false
		for _, tm := range t.Moods {
			if strings.EqualFold(strings.TrimSpace(tm), mood) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		p := t.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(cfg.Library, p)
		}
		md, err := r.Validator.Validate(ctx, p, "audio")
		if err != nil {
			logx.From(ctx).Warn("audio.music.invalid", "source", SourceMusicLocal, "path", p, "error", err)
			continue
		}
		return Asset{EventID: "music", Keyword: firstNonEmpty(t.Title, filepath.Base(p)), Query: "mood:" + mood, Path: p,
			Source: SourceMusicLocal, MediaType: "audio", SourceURL: t.SourceURL,
			License: firstNonEmpty(t.License, "local (fornecido pelo usuário)"), Author: t.Author,
			Attribution: strings.Join(nonEmpty(t.Title, t.Author, t.License), " · "), Duration: md.Duration}, true
	}
	return Asset{}, false
}

type openverseAudio struct {
	Results []struct {
		ID                string   `json:"id"`
		Title             string   `json:"title"`
		URL               string   `json:"url"`
		Creator           string   `json:"creator"`
		License           string   `json:"license"`
		LicenseVersion    string   `json:"license_version"`
		ForeignLandingURL string   `json:"foreign_landing_url"`
		Duration          int      `json:"duration"` // milliseconds
		Genres            []string `json:"genres"`
		Filetype          string   `json:"filetype"`
		Provider          string   `json:"provider"`
		Tags              []struct {
			Name string `json:"name"`
		} `json:"tags"`
	} `json:"results"`
}

// OpenverseLicense maps Openverse licence codes to display names.
func OpenverseLicense(code, version string) string {
	switch strings.ToLower(code) {
	case "cc0":
		return "CC0"
	case "pdm":
		return "Public domain"
	case "by", "by-sa":
		return strings.TrimSpace("CC " + strings.ToUpper(code) + " " + version)
	}
	return strings.ToUpper(code)
}

// openverseMusic searches Openverse (open source, WordPress Foundation) for
// music allowed in commercial, modified works, and returns the best track.
func (r *Resolver) openverseMusic(ctx context.Context, mood, query string) (Asset, error) {
	logger := logx.From(ctx)
	u, err := url.Parse(strings.TrimRight(r.OpenverseAPI, "/") + "/audio/")
	if err != nil {
		return Asset{}, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("license_type", "commercial,modification")
	q.Set("category", "music")
	q.Set("page_size", "20")
	q.Set("mature", "false")
	u.RawQuery = q.Encode()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Asset{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return Asset{}, transientErr{fmt.Errorf("Openverse: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Asset{}, transientErr{fmt.Errorf("Openverse HTTP %s", resp.Status)}
	}
	var or openverseAudio
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&or); err != nil {
		return Asset{}, fmt.Errorf("Openverse JSON: %w", err)
	}
	logger.Info("audio.music.search", "provider", SourceOpenverse, "query", query, "results", len(or.Results), "duration_ms", time.Since(started).Milliseconds())

	type cand struct {
		i     int
		score float64
		lic   string
	}
	var cs []cand
	for i, x := range or.Results {
		lic := OpenverseLicense(x.License, x.LicenseVersion)
		dur := float64(x.Duration) / 1000
		tags := make([]string, 0, len(x.Tags)+len(x.Genres))
		tags = append(tags, x.Genres...)
		for _, t := range x.Tags {
			tags = append(tags, t.Name)
		}
		text := x.Title + " " + strings.Join(tags, " ")
		genreHits := anchorHits(moodGenres[mood], text)
		rel := relevance(query, text, "")
		reject := ""
		switch {
		case x.URL == "":
			reject = "sem url"
		case !LicenseOK(lic):
			reject = "licença " + lic
		case dur > 0 && dur < minMusicDurationS:
			reject = fmt.Sprintf("curta %.0fs", dur)
		case dur > 600:
			reject = "longa demais"
		case rel < 0.25 && genreHits == 0:
			reject = "fora do mood"
		}
		if reject != "" {
			logger.Debug("audio.music.candidate", "title", x.Title, "accepted", false, "reason", reject)
			continue
		}
		score := rel + 0.15*float64(minI(genreHits, 3)) - float64(i)*0.01
		if dur >= 45 && dur <= 240 {
			score += 0.15 // long enough to cover a reel without obvious loops
		}
		cs = append(cs, cand{i, score, lic})
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].score > cs[b].score })
	for k, c := range cs {
		if k >= r.MaxCandidates {
			break
		}
		x := or.Results[c.i]
		path, m, err := r.download(ctx, SourceOpenverse, x.URL, "audio")
		if err != nil {
			logger.Info("audio.music.invalid", "title", x.Title, "url", x.URL, "error", err)
			continue
		}
		return Asset{Keyword: x.Title, Query: query, Path: path, Source: SourceOpenverse, MediaType: "audio", URL: x.URL,
			SourceURL: x.ForeignLandingURL, License: c.lic, Author: x.Creator,
			Attribution: strings.Join(nonEmpty(x.Title, x.Creator, c.lic, x.Provider, "via Openverse"), " · "),
			Duration:    m.Duration}, nil
	}
	return Asset{}, fmt.Errorf("nenhuma trilha válida para %q (%d resultados)", query, len(or.Results))
}
