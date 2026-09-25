package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Result struct{ Stdout, Stderr string }

func Run(ctx context.Context, stdout io.Writer, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, er bytes.Buffer
	if stdout != nil {
		cmd.Stdout = io.MultiWriter(stdout, &out)
	} else {
		cmd.Stdout = &out
	}
	cmd.Stderr = &er
	err := cmd.Run()
	if err != nil {
		return Result{out.String(), er.String()}, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail(er.String(), 5000))
	}
	return Result{out.String(), er.String()}, nil
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
