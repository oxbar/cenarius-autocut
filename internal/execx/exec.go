package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"cenarius-autocut/internal/logx"
)

type Result struct{ Stdout, Stderr string }

func Run(ctx context.Context, stdout io.Writer, name string, args ...string) (Result, error) {
	logger := logx.From(ctx)
	started := time.Now()
	logger.Debug("exec.start", "command", name, "args", args)

	cmd := exec.CommandContext(ctx, name, args...)
	var out, er bytes.Buffer
	if stdout != nil {
		cmd.Stdout = io.MultiWriter(stdout, &out)
	} else {
		cmd.Stdout = &out
	}
	cmd.Stderr = &er
	err := cmd.Run()
	dur := time.Since(started)
	result := Result{out.String(), er.String()}

	// Keep the complete subprocess output in debug.log. ffmpeg and whisper put
	// most diagnostics on stderr even when the command succeeds.
	logger.Debug("exec.output",
		"command", name,
		"duration_ms", dur.Milliseconds(),
		"stdout", sanitizeLog(result.Stdout),
		"stderr", sanitizeLog(result.Stderr),
	)

	if err != nil {
		logger.Error("exec.failed", "command", name, "duration_ms", dur.Milliseconds(), "error", err)
		return result, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail(er.String(), 5000))
	}
	logger.Debug("exec.done", "command", name, "duration_ms", dur.Milliseconds())
	return result, nil
}

func LookPath(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("dependência não encontrada: %s", name)
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

var sensitiveMetadataRE = regexp.MustCompile(`(?mi)^.*(?:com\.apple\.quicktime\.location|\blocation(?:-eng)?\s*[:=]|\bgps[\w.-]*|\blatitude\b|\blongitude\b|ISO6709)[^\n]*\n?`)

// iso6709RE catches bare coordinate strings such as "+23.5505-046.6333+760.000/".
var iso6709RE = regexp.MustCompile(`[+-]\d{1,2}\.\d{3,}[+-]\d{1,3}\.\d{3,}(?:[+-]\d+(?:\.\d+)?)?/?`)

// sanitizeLog keeps diagnostics useful without persisting precise location
// metadata embedded by iPhone MOV files (QuickTime location, GPS, latitude,
// longitude, ISO 6709 coordinates). The raw subprocess result returned to the
// caller is unchanged; only debug.log is redacted.
func sanitizeLog(s string) string {
	s = sensitiveMetadataRE.ReplaceAllString(s, "[redacted location metadata]\n")
	return iso6709RE.ReplaceAllString(s, "[redacted coordinates]")
}

// SanitizeLog exposes the redaction for other log writers and tests.
func SanitizeLog(s string) string { return sanitizeLog(s) }
