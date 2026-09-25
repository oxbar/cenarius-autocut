package assets

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var filterCache sync.Map // "bin|filter" -> bool

// HasFilterFunc can be replaced in tests to simulate FFmpeg builds without
// drawtext/libass (e.g. Homebrew's plain `ffmpeg`, which lacks libfreetype).
var HasFilterFunc = hasFilter

// FFmpegHasFilter reports whether the given ffmpeg binary provides a filter.
func FFmpegHasFilter(bin, name string) bool { return HasFilterFunc(bin, name) }

func hasFilter(bin, name string) bool {
	if bin == "" {
		bin = "ffmpeg"
	}
	key := bin + "|" + name
	if v, ok := filterCache.Load(key); ok {
		return v.(bool)
	}
	out, err := exec.Command(bin, "-hide_banner", "-filters").Output()
	found := false
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[1] == name {
				found = true
				break
			}
		}
	}
	filterCache.Store(key, found)
	return found
}

// FindFFmpeg returns the best local FFmpeg/FFprobe pair: $FFMPEG_BIN /
// $FFPROBE_BIN, then Homebrew ffmpeg-full (which has libass + drawtext), then
// PATH. Used by tests and tooling; the pipeline uses config.json.
func FindFFmpeg() (string, string) {
	if b := os.Getenv("FFMPEG_BIN"); b != "" {
		p := os.Getenv("FFPROBE_BIN")
		if p == "" {
			p = strings.TrimSuffix(b, "ffmpeg") + "ffprobe"
		}
		return b, p
	}
	for _, dir := range []string{"/opt/homebrew/opt/ffmpeg-full/bin", "/usr/local/opt/ffmpeg-full/bin"} {
		if _, err := os.Stat(dir + "/ffmpeg"); err == nil {
			return dir + "/ffmpeg", dir + "/ffprobe"
		}
	}
	b, _ := exec.LookPath("ffmpeg")
	p, _ := exec.LookPath("ffprobe")
	return b, p
}
