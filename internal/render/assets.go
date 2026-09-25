package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Asset struct {
	Keyword string   `json:"keyword"`
	Aliases []string `json:"aliases"`
	File    string   `json:"file"`
}
type Manifest struct {
	Assets []Asset `json:"assets"`
}

func LoadManifest(path, assetDir string) (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, a := range m.Assets {
		p := a.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(assetDir, p)
		}
		out[strings.ToLower(a.Keyword)] = p
		for _, x := range a.Aliases {
			out[strings.ToLower(x)] = p
		}
	}
	return out, nil
}
