package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"cenarius-autocut/internal/execx"
)

type Info struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	HasAudio bool    `json:"has_audio"`
}

type ffprobeRoot struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func Probe(ctx context.Context, ffprobe, path string) (Info, error) {
	r, err := execx.Run(ctx, nil, ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	if err != nil {
		return Info{}, err
	}
	var root ffprobeRoot
	if err := json.Unmarshal([]byte(r.Stdout), &root); err != nil {
		return Info{}, err
	}
	d, _ := strconv.ParseFloat(root.Format.Duration, 64)
	info := Info{Duration: d}
	for _, s := range root.Streams {
		switch s.CodecType {
		case "video":
			info.Width, info.Height = s.Width, s.Height
			info.FPS = parseRate(s.RFrameRate)
		case "audio":
			info.HasAudio = true
		}
	}
	if info.Width == 0 || info.Height == 0 {
		return Info{}, fmt.Errorf("nenhum stream de vídeo encontrado")
	}
	return info, nil
}

func parseRate(s string) float64 {
	var a, b float64
	if _, err := fmt.Sscanf(s, "%f/%f", &a, &b); err == nil && b != 0 {
		return a / b
	}
	return 0
}

func ExtractAudio(ctx context.Context, ffmpeg, input, wav string) error {
	_, err := execx.Run(ctx, nil, ffmpeg, "-y", "-i", input, "-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav)
	return err
}
