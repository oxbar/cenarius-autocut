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
	"regexp"
	"sort"
	"strings"
	"time"

	"cenarius-autocut/internal/logx"
)

const defaultCommonsAPI = "https://commons.wikimedia.org/w/api.php"
const userAgent = "CENARIUS-AutoCut/1.6 (local open-source video editor; https://github.com/oxbar/cenarius-autocut)"

type extMeta map[string]struct {
	Value string `json:"value"`
}

type commonsPage struct {
	PageID    int    `json:"pageid"`
	Title     string `json:"title"`
	ImageInfo []struct {
		URL      string  `json:"url"`
		ThumbURL string  `json:"thumburl"`
		Mime     string  `json:"mime"`
		Size     int64   `json:"size"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
		Duration float64 `json:"duration"`
		Ext      extMeta `json:"extmetadata"`
	} `json:"imageinfo"`
}

type commonsResp struct {
	Query struct {
		Pages []commonsPage `json:"pages"`
	} `json:"query"`
}

// Candidate is one remote file that passed metadata filtering.
type Candidate struct {
	Title    string
	Text     string // provider description: alt text, tags or URL slug (semantic gate)
	Page     string
	URLs     []string // download attempts in order (derivatives/thumbs first)
	Mime     string
	Kind     string // video | image
	License  string
	Author   string
	Credit   string
	Width    int
	Height   int
	Duration float64
	Score    float64
}

var stopwords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "on": true, "in": true, "and": true, "at": true, "with": true, "for": true, "to": true, "de": true, "da": true, "do": true, "e": true}

// Titles that are almost never good B-roll (fine for cards).
var nonBrollTitleRE = regexp.MustCompile(`(?i)(logo|icon|map of|locator|diagram|chart|graph|flag of|coat of arms|seal of|wordmark|\.svg|\.pdf|\.djvu|\.tif)`)

func tokenize(s string) []string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^\pL\pN]+`).ReplaceAllString(s, " ")
	var out []string
	for _, w := range strings.Fields(s) {
		if !stopwords[w] && len(w) > 1 {
			out = append(out, w)
		}
	}
	return out
}

// relevance is the share of query words that appear in the file title or
// description (with a light plural/prefix tolerance).
func relevance(query, title, desc string) float64 {
	q := tokenize(query)
	if len(q) == 0 {
		return 0
	}
	hay := tokenize(title + " " + desc)
	hit := 0
	for _, w := range q {
		for _, h := range hay {
			if h == w || (len(w) >= 5 && (strings.HasPrefix(h, w[:len(w)-1]) || strings.HasPrefix(w, h) && len(h) >= 5)) {
				hit++
				break
			}
		}
	}
	return float64(hit) / float64(len(q))
}

// LicenseOK accepts only licences that allow reuse with modification in a
// public video: public domain / CC0 / CC BY / CC BY-SA. Unknown, NC and ND
// licences are rejected.
func LicenseOK(short string) bool {
	l := strings.ToLower(strings.TrimSpace(short))
	if l == "" {
		return false
	}
	if strings.Contains(l, "nc") && strings.Contains(l, "cc") || strings.Contains(l, "-nd") || strings.Contains(l, " nd") || strings.Contains(l, "noncommercial") || strings.Contains(l, "no deriv") {
		return false
	}
	switch {
	case strings.Contains(l, "public domain"), strings.HasPrefix(l, "pd"), strings.Contains(l, "cc0"), strings.Contains(l, "cc-zero"):
		return true
	case strings.HasPrefix(l, "cc by"), strings.HasPrefix(l, "cc-by"), strings.HasPrefix(l, "cc-by-sa"), strings.HasPrefix(l, "cc by-sa"):
		return true
	case strings.HasPrefix(l, "attribution"), strings.Contains(l, "gfdl"), strings.HasPrefix(l, "fal"), strings.Contains(l, "free art"):
		return true
	}
	return false
}

// searchCommons runs one Commons search for a query and returns filtered
// candidates sorted by relevance.
func (r *Resolver) searchCommons(ctx context.Context, query, kind, layout string) ([]Candidate, error) {
	logger := logx.From(ctx)
	u, err := url.Parse(r.API)
	if err != nil {
		return nil, err
	}
	filter := " filetype:bitmap"
	if kind == "video" {
		filter = " filetype:video"
	}
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("generator", "search")
	q.Set("gsrsearch", query+filter)
	q.Set("gsrnamespace", "6")
	q.Set("gsrlimit", "20")
	q.Set("prop", "imageinfo")
	q.Set("iiprop", "url|mime|size|extmetadata")
	q.Set("iiurlwidth", "1280")
	q.Set("iiextmetadatafilter", "LicenseShortName|Artist|Credit|ImageDescription|ObjectName")
	u.RawQuery = q.Encode()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Wikimedia Commons: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Wikimedia Commons HTTP %s", resp.Status)
	}
	var cr commonsResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&cr); err != nil {
		return nil, fmt.Errorf("Wikimedia Commons JSON: %w", err)
	}
	logger.Info("visual.asset.search", "provider", "wikimedia-commons", "query", query, "kind", kind, "results", len(cr.Query.Pages), "duration_ms", time.Since(started).Milliseconds())

	var out []Candidate
	for _, p := range cr.Query.Pages {
		if len(p.ImageInfo) == 0 {
			continue
		}
		ii := p.ImageInfo[0]
		mime := strings.ToLower(strings.TrimSpace(strings.Split(ii.Mime, ";")[0]))
		isVideo := isVideoMime(mime)
		reject := ""
		switch {
		case !supportedMime(mime):
			reject = "mime " + mime
		case (kind == "video") != isVideo:
			reject = "kind"
		case ii.Size > r.MaxBytes && !isVideo:
			reject = "too large"
		case !LicenseOK(meta(ii.Ext, "LicenseShortName")):
			reject = "license " + meta(ii.Ext, "LicenseShortName")
		case !isVideo && ii.Width > 0 && (ii.Width < 480 || ii.Height < 320):
			reject = fmt.Sprintf("small %dx%d", ii.Width, ii.Height)
		case isVideo && ii.Duration > 0 && ii.Duration < 1.0:
			reject = "video too short"
		case layout != "card" && layout != "pip" && nonBrollTitleRE.MatchString(p.Title):
			reject = "not b-roll (logo/map/diagram)"
		}
		desc := stripHTML(meta(ii.Ext, "ImageDescription")) + " " + stripHTML(meta(ii.Ext, "ObjectName"))
		score := relevance(query, p.Title, desc)
		if reject == "" && score < 0.34 {
			reject = fmt.Sprintf("low relevance %.2f", score)
		}
		if reject != "" {
			logger.Debug("visual.asset.candidate", "title", p.Title, "accepted", false, "reason", reject)
			continue
		}
		c := Candidate{
			Title: p.Title, Page: "https://commons.wikimedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(p.Title, " ", "_")),
			Mime: mime, Kind: map[bool]string{true: "video", false: "image"}[isVideo],
			License: meta(ii.Ext, "LicenseShortName"), Author: stripHTML(meta(ii.Ext, "Artist")), Credit: stripHTML(meta(ii.Ext, "Credit")),
			Width: ii.Width, Height: ii.Height, Duration: ii.Duration, Score: score,
		}
		if isVideo {
			c.URLs = append(c.URLs, r.videoDerivatives(ctx, p.Title)...)
			c.URLs = append(c.URLs, transcodeGuesses(ii.URL)...)
			if ii.Size <= r.MaxBytes {
				c.URLs = append(c.URLs, ii.URL)
			}
		} else {
			if ii.ThumbURL != "" {
				c.URLs = append(c.URLs, ii.ThumbURL)
			}
			c.URLs = append(c.URLs, ii.URL)
		}
		c.URLs = dedupe(c.URLs)
		if len(c.URLs) == 0 {
			continue
		}
		logger.Info("visual.asset.candidate", "title", p.Title, "kind", c.Kind, "score", score, "license", c.License, "urls", len(c.URLs))
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// videoDerivatives asks TimedMediaHandler for web-friendly transcodes
// (480p/720p WebM/MP4) so we don't download a 4K original.
func (r *Resolver) videoDerivatives(ctx context.Context, title string) []string {
	u, err := url.Parse(r.API)
	if err != nil {
		return nil
	}
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("titles", title)
	q.Set("prop", "videoinfo")
	q.Set("viprop", "derivatives")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil || resp.StatusCode/100 != 2 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var vr struct {
		Query struct {
			Pages []struct {
				VideoInfo []struct {
					Derivatives []struct {
						Src    string `json:"src"`
						Type   string `json:"type"`
						Height int    `json:"height"`
					} `json:"derivatives"`
				} `json:"videoinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&vr) != nil {
		return nil
	}
	type d struct {
		src  string
		dist int
	}
	var ds []d
	for _, p := range vr.Query.Pages {
		for _, vi := range p.VideoInfo {
			for _, x := range vi.Derivatives {
				t := strings.ToLower(x.Type)
				if x.Src == "" || !(strings.Contains(t, "webm") || strings.Contains(t, "mp4")) || x.Height < 300 || x.Height > 1100 {
					continue
				}
				dist := x.Height - 720
				if dist < 0 {
					dist = -dist
				}
				ds = append(ds, d{x.Src, dist})
			}
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].dist < ds[j].dist })
	out := make([]string, 0, len(ds))
	for _, x := range ds {
		out = append(out, x.src)
	}
	return out
}

// transcodeGuesses builds the conventional upload.wikimedia.org transcode
// URLs. They are only attempts: a 404 or HTML answer is rejected by download.
func transcodeGuesses(orig string) []string {
	const marker = "/wikipedia/commons/"
	i := strings.Index(orig, marker)
	if i < 0 || strings.Contains(orig, "/transcoded/") {
		return nil
	}
	rest := orig[i+len(marker):]
	name := rest[strings.LastIndex(rest, "/")+1:]
	base := orig[:i+len(marker)] + "transcoded/" + rest + "/" + name
	return []string{base + ".720p.vp9.webm", base + ".480p.vp9.webm", base + ".720p.webm", base + ".480p.webm"}
}

// download fetches one URL into the cache and validates it. A non-2xx status,
// an HTML body or an undecodable file is rejected.
func (r *Resolver) download(ctx context.Context, source, rawURL, kind string) (string, Media, error) {
	logger := logx.From(ctx)
	if p, ok := r.cache.FindByURL(source, rawURL); ok {
		m, err := r.Validator.Validate(ctx, p, kind)
		if err == nil {
			logger.Info("visual.asset.cache_hit", "level", "media", "url", rawURL, "path", p)
			return p, m, nil
		}
		_ = os.Remove(p)
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", Media{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", Media{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", Media{}, fmt.Errorf("HTTP %s", resp.Status)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/json") {
		return "", Media{}, fmt.Errorf("resposta HTML/JSON em vez de mídia (%s)", ct)
	}
	if resp.ContentLength > r.MaxBytes {
		return "", Media{}, fmt.Errorf("arquivo grande demais: %d bytes", resp.ContentLength)
	}
	ext := extForMime(ct)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(strings.Split(rawURL, "?")[0]))
	}
	if ext == "" {
		ext = map[string]string{"video": ".webm", "image": ".jpg", "audio": ".mp3"}[kind]
	}
	final := r.cache.MediaPath(source, rawURL, ext)
	tmp := final + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", Media{}, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, r.MaxBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n > r.MaxBytes {
		_ = os.Remove(tmp)
		if copyErr == nil && closeErr == nil {
			copyErr = fmt.Errorf("excedeu limite de %d bytes", r.MaxBytes)
		}
		if copyErr == nil {
			copyErr = closeErr
		}
		return "", Media{}, copyErr
	}
	logger.Info("visual.asset.download", "url", rawURL, "bytes", n, "duration_ms", time.Since(started).Milliseconds())
	m, err := r.Validator.Validate(ctx, tmp+"", kind)
	if err != nil {
		_ = os.Remove(tmp)
		return "", Media{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", Media{}, err
	}
	return final, m, nil
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
