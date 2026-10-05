package assembly

import (
	"cenarius-autocut/internal/captions"
	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/transcribe"
)

func captionsASS(path string, cfg config.Config) error {
	return captions.GenerateASS(path, []transcribe.Token{{Text: " olá", Start: 0.2, End: 0.6}, {Text: " mundo", Start: 0.7, End: 1.1}}, cfg.Captions, nil)
}
