package captions

import (
	"cenarius-autocut/internal/transcribe"
	"testing"
)

func TestGroup(t *testing.T) {
	ts := []transcribe.Token{{Text: "olá", Start: 0, End: .2}, {Text: "mundo.", Start: .21, End: .5}, {Text: "teste", Start: 1.2, End: 1.5}}
	c := Group(ts, 5)
	if len(c) != 2 {
		t.Fatalf("%v", c)
	}
}
