package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileDoesNotOverrideEnvironment(t *testing.T) {
	key := "CENARIUS_TEST_ENV_PRECEDENCE"
	t.Setenv(key, "shell")
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(key+"=file\nCENARIUS_TEST_ENV_NEW='loaded'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("CENARIUS_TEST_ENV_NEW")
	t.Cleanup(func() { _ = os.Unsetenv("CENARIUS_TEST_ENV_NEW") })
	if err := LoadEnvFile(p); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "shell" {
		t.Fatalf("existing env overwritten: %q", got)
	}
	if got := os.Getenv("CENARIUS_TEST_ENV_NEW"); got != "loaded" {
		t.Fatalf("env file value not loaded: %q", got)
	}
}
