package assets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"cenarius-autocut/internal/logx"
)

const (
	defaultPexelsAPI  = "https://api.pexels.com/v1"
	defaultPixabayAPI = "https://pixabay.com/api"
)

// searchPexels uses only the API key held in memory/environment. The key is
// sent in the Authorization header and is never included in logs or URLs.
func (r *Resolver) searchPexels(ctx context.Context, query, kind, layout string) ([]Candidate, error) {
	if strings.TrimSpace(r.PexelsKey) == "" {
		return nil, fmt.Errorf("PEXELS_API_KEY não configurada")
	}
	base := strings.TrimRight(r.PexelsAPI, "/")
	path := "/search"
	if kind == "video" {
		path = "/videos/search"
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("orientation", "portrait")
	q.Set("size", "medium")
	q.Set("per_page", "20")
	u.RawQuery = q.Encode()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", r.PexelsKey)
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Pexels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Pexels HTTP %s", resp.Status)
	}
	var out []Candidate
	if kind == "video" {
		var body struct {
			Videos []struct {
				ID       int     `json:"id"`
				Width    int     `json:"width"`
				Height   int     `json:"height"`
				URL      string  `json:"url"`
				Duration float64 `json:"duration"`
				User     struct {
					Name string `json:"name"`
				} `json:"user"`
				Files []struct {
					Quality  string `json:"quality"`
					FileType string `json:"file_type"`
					Width    int    `json:"width"`
					Height   int    `json:"height"`
					Link     string `json:"link"`
				} `json:"video_files"`
			} `json:"videos"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
			return nil, fmt.Errorf("Pexels JSON: %w", err)
		}
		for i, v := range body.Videos {
			type rankedURL struct {
				u     string
				score float64
			}
			var rs []rankedURL
			for _, f := range v.Files {
				if f.Link == "" || !strings.Contains(strings.ToLower(f.FileType), "mp4") || f.Width < 240 || f.Height < 240 {
					continue
				}
				score := stockShapeScore(f.Width, f.Height, v.Duration)
				if strings.EqualFold(f.Quality, "hd") {
					score += 0.05
				}
				rs = append(rs, rankedURL{f.Link, score})
			}
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].score > rs[j].score })
			urls := make([]string, 0, len(rs))
			for _, x := range rs {
				urls = append(urls, x.u)
			}
			if len(urls) == 0 {
				continue
			}
			out = append(out, Candidate{Title: "Pexels video " + strconv.Itoa(v.ID), Text: slugText(v.URL), Page: v.URL, URLs: dedupe(urls), Mime: "video/mp4", Kind: "video",
				License: "Pexels License", Author: v.User.Name, Credit: "Pexels", Width: v.Width, Height: v.Height, Duration: v.Duration,
				Score: stockSearchScore(v.Width, v.Height, v.Duration, i)})
		}
	} else {
		var body struct {
			Photos []struct {
				ID           int    `json:"id"`
				Width        int    `json:"width"`
				Height       int    `json:"height"`
				URL          string `json:"url"`
				Photographer string `json:"photographer"`
				Alt          string `json:"alt"`
				Src          struct {
					Original string `json:"original"`
					Large2X  string `json:"large2x"`
					Large    string `json:"large"`
					Portrait string `json:"portrait"`
				} `json:"src"`
			} `json:"photos"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
			return nil, fmt.Errorf("Pexels JSON: %w", err)
		}
		for i, p := range body.Photos {
			urls := dedupe([]string{p.Src.Portrait, p.Src.Large2X, p.Src.Large, p.Src.Original})
			if len(urls) == 0 || p.Width < 480 || p.Height < 480 {
				continue
			}
			out = append(out, Candidate{Title: firstNonEmpty(p.Alt, "Pexels photo "+strconv.Itoa(p.ID)), Text: slugText(p.URL), Page: p.URL, URLs: urls, Mime: "image/jpeg", Kind: "image",
				License: "Pexels License", Author: p.Photographer, Credit: "Pexels", Width: p.Width, Height: p.Height,
				Score: stockSearchScore(p.Width, p.Height, 0, i)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	logx.From(ctx).Info("visual.asset.search", "provider", SourcePexels, "query", query, "kind", kind, "results", len(out), "duration_ms", time.Since(started).Milliseconds())
	return out, nil
}

// searchPixabay keeps the API key exclusively in the search request URL. We
// never log that URL, only provider/query/result metadata; media downloads use
// the returned CDN URLs which do not contain the API key.
func (r *Resolver) searchPixabay(ctx context.Context, query, kind, layout string) ([]Candidate, error) {
	if strings.TrimSpace(r.PixabayKey) == "" {
		return nil, fmt.Errorf("PIXABAY_API_KEY não configurada")
	}
	base := strings.TrimRight(r.PixabayAPI, "/")
	path := "/"
	if kind == "video" {
		path = "/videos/"
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("key", r.PixabayKey)
	q.Set("q", query)
	q.Set("safesearch", "true")
	q.Set("per_page", "20")
	q.Set("lang", "en")
	if kind == "image" {
		q.Set("orientation", "vertical")
		q.Set("image_type", "photo")
		q.Set("min_width", "640")
	}
	u.RawQuery = q.Encode()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Pixabay: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Pixabay HTTP %s", resp.Status)
	}
	var out []Candidate
	if kind == "video" {
		var body struct {
			Hits []struct {
				ID       int     `json:"id"`
				PageURL  string  `json:"pageURL"`
				Duration float64 `json:"duration"`
				User     string  `json:"user"`
				Tags     string  `json:"tags"`
				Videos   map[string]struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
					Size   int64  `json:"size"`
				} `json:"videos"`
			} `json:"hits"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
			return nil, fmt.Errorf("Pixabay JSON: %w", err)
		}
		for i, h := range body.Hits {
			type rankedURL struct {
				u     string
				w, h  int
				score float64
			}
			var rs []rankedURL
			for _, f := range h.Videos {
				if f.URL == "" || f.Width < 240 || f.Height < 240 || (f.Size > 0 && f.Size > r.MaxBytes) {
					continue
				}
				rs = append(rs, rankedURL{f.URL, f.Width, f.Height, stockShapeScore(f.Width, f.Height, h.Duration)})
			}
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].score > rs[j].score })
			if len(rs) == 0 {
				continue
			}
			urls := make([]string, 0, len(rs))
			for _, x := range rs {
				urls = append(urls, x.u)
			}
			out = append(out, Candidate{Title: firstNonEmpty(h.Tags, "Pixabay video "+strconv.Itoa(h.ID)), Text: slugText(h.PageURL), Page: h.PageURL, URLs: dedupe(urls), Mime: "video/mp4", Kind: "video",
				License: "Pixabay Content License", Author: h.User, Credit: "Pixabay", Width: rs[0].w, Height: rs[0].h, Duration: h.Duration,
				Score: stockSearchScore(rs[0].w, rs[0].h, h.Duration, i)})
		}
	} else {
		var body struct {
			Hits []struct {
				ID            int    `json:"id"`
				PageURL       string `json:"pageURL"`
				User          string `json:"user"`
				Tags          string `json:"tags"`
				LargeImageURL string `json:"largeImageURL"`
				WebformatURL  string `json:"webformatURL"`
				ImageWidth    int    `json:"imageWidth"`
				ImageHeight   int    `json:"imageHeight"`
			} `json:"hits"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
			return nil, fmt.Errorf("Pixabay JSON: %w", err)
		}
		for i, h := range body.Hits {
			urls := dedupe([]string{h.LargeImageURL, h.WebformatURL})
			if len(urls) == 0 || h.ImageWidth < 480 || h.ImageHeight < 480 {
				continue
			}
			out = append(out, Candidate{Title: firstNonEmpty(h.Tags, "Pixabay image "+strconv.Itoa(h.ID)), Text: slugText(h.PageURL), Page: h.PageURL, URLs: urls, Mime: "image/jpeg", Kind: "image",
				License: "Pixabay Content License", Author: h.User, Credit: "Pixabay", Width: h.ImageWidth, Height: h.ImageHeight,
				Score: stockSearchScore(h.ImageWidth, h.ImageHeight, 0, i)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	logx.From(ctx).Info("visual.asset.search", "provider", SourcePixabay, "query", query, "kind", kind, "results", len(out), "duration_ms", time.Since(started).Milliseconds())
	return out, nil
}

func stockSearchScore(w, h int, duration float64, index int) float64 {
	s := 0.60 + stockShapeScore(w, h, duration)
	// Search APIs are already relevance-ranked; preserve that signal lightly.
	s -= float64(index) * 0.008
	return s
}

func stockShapeScore(w, h int, duration float64) float64 {
	s := 0.0
	if h > w {
		s += 0.18
	} else if h == w {
		s += 0.08
	}
	if w >= 720 || h >= 1280 {
		s += 0.08
	} else if w >= 480 || h >= 854 {
		s += 0.04
	}
	if duration >= 2 && duration <= 30 {
		s += 0.06
	}
	return s
}
