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

var sensitiveMetadataRE = regexp.MustCompile(`(?m)^.*com\.apple\.quicktime\.location(?:\.ISO6709|\.accuracy\.horizontal)?[^\n]*\n?`)

// sanitizeLog keeps diagnostics useful without persisting precise location
// metadata embedded by iPhone MOV files. The raw subprocess result returned to
// the caller is unchanged; only debug.log is redacted.
func sanitizeLog(s string) string {
	return sensitiveMetadataRE.ReplaceAllString(s, "[redacted iPhone location metadata]\n")
}
