package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cache layout (data/asset-cache/):
//
//	media/<sha256(source|url)>.<ext>   downloaded file, shared by every query
//	index/<sha256(kind|query)>.json    query -> Asset (includes query + URL)
//	neg/<sha256(kind|query)>.json      "no result" marker (TTL) to avoid re-searching
//	procedural/<sha256(label|w|h)>.mp4 generated motion graphics
//
// Keys combine the query with the media URL/source, so the same file is never
// downloaded twice even when several queries resolve to it.
type Cache struct {
	Dir    string
	NegTTL time.Duration
}

func hashKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.Join(parts, "|"))))
	return hex.EncodeToString(h[:12])
}

func normQuery(q string) string { return strings.Join(strings.Fields(strings.ToLower(q)), " ") }

func (c Cache) MediaPath(source, url, ext string) string {
	return filepath.Join(c.Dir, "media", hashKey(source, url)+ext)
}

func (c Cache) indexPath(kind, query string) string {
	return filepath.Join(c.Dir, "index", hashKey(kind, normQuery(query))+".json")
}

func (c Cache) negPath(kind, query string) string {
	return filepath.Join(c.Dir, "neg", hashKey(kind, normQuery(query))+".json")
}

func (c Cache) ProceduralPath(label string, w, h int) string {
	return filepath.Join(c.Dir, "procedural", hashKey("procedural-v1", label, itoa(w), itoa(h))+".mp4")
}

func (c Cache) Ensure() error {
	for _, d := range []string{"media", "index", "neg", "procedural"} {
		if err := os.MkdirAll(filepath.Join(c.Dir, d), 0755); err != nil {
			return err
		}
	}
	return nil
}

// Lookup returns a previously resolved asset for (kind, query) whose file
// still exists.
func (c Cache) Lookup(kind, query string) (Asset, bool) {
	b, err := os.ReadFile(c.indexPath(kind, query))
	if err != nil {
		return Asset{}, false
	}
	var a Asset
	if json.Unmarshal(b, &a) != nil || a.Path == "" {
		return Asset{}, false
	}
	if st, err := os.Stat(a.Path); err != nil || st.Size() == 0 {
		return Asset{}, false
	}
	return a, true
}

func (c Cache) Store(kind, query string, a Asset) {
	b, _ := json.MarshalIndent(a, "", "  ")
	_ = os.WriteFile(c.indexPath(kind, query), b, 0644)
}

// FindByURL returns an existing downloaded file for a source URL.
func (c Cache) FindByURL(source, url string) (string, bool) {
	matches, _ := filepath.Glob(filepath.Join(c.Dir, "media", hashKey(source, url)+".*"))
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Size() > 0 && !strings.HasSuffix(m, ".part") {
			return m, true
		}
	}
	return "", false
}

func (c Cache) MarkMiss(kind, query string) {
	b, _ := json.Marshal(map[string]any{"query": query, "at": time.Now().Unix()})
	_ = os.WriteFile(c.negPath(kind, query), b, 0644)
}

func (c Cache) RecentMiss(kind, query string) bool {
	ttl := c.NegTTL
	if ttl <= 0 {
		return false
	}
	st, err := os.Stat(c.negPath(kind, query))
	return err == nil && time.Since(st.ModTime()) < ttl
}
