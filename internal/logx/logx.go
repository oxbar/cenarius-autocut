package logx

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type ctxKey struct{}

var discard = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

// StartJob creates a per-job debug.log and attaches a DEBUG logger to the context.
// The log is intentionally verbose: commands, timings, ffmpeg/whisper stderr,
// media metadata and planner decisions are recorded so a bad render can be replayed.
func StartJob(ctx context.Context, outDir string) (context.Context, string, io.Closer, error) {
	path := filepath.Join(outDir, "debug.log")
	f, err := os.Create(path)
	if err != nil {
		return ctx, "", nil, err
	}
	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})
	l := slog.New(h)
	return context.WithValue(ctx, ctxKey{}, l), path, f, nil
}

func From(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return discard
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return discard
}
