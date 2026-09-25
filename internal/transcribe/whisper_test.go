package transcribe

import (
	"encoding/json"
	"testing"
)

func TestParseJSONMergesBPETokenPieces(t *testing.T) {
	raw := map[string]any{
		"result": map[string]any{"language": "pt"},
		"transcription": []any{
			map[string]any{
				"offsets": map[string]any{"from": 0, "to": 2500},
				"text":    " testando assuntos.",
				"tokens": []any{
					map[string]any{"text": " test", "offsets": map[string]any{"from": 0, "to": 400}, "p": 0.9},
					map[string]any{"text": "ando", "offsets": map[string]any{"from": 400, "to": 900}, "p": 0.9},
					map[string]any{"text": " ass", "offsets": map[string]any{"from": 1000, "to": 1300}, "p": 0.9},
					map[string]any{"text": "unt", "offsets": map[string]any{"from": 1300, "to": 1600}, "p": 0.9},
					map[string]any{"text": "os", "offsets": map[string]any{"from": 1600, "to": 1900}, "p": 0.9},
					map[string]any{"text": ".", "offsets": map[string]any{"from": 1900, "to": 2000}, "p": 0.9},
					map[string]any{"text": "[_TT_100]", "offsets": map[string]any{"from": 2000, "to": 2000}, "p": 0.1},
				},
			},
		},
	}
	b, _ := json.Marshal(raw)
	tr, err := ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Tokens) != 2 {
		t.Fatalf("tokens=%#v", tr.Tokens)
	}
	if tr.Tokens[0].Text != "testando" {
		t.Fatalf("first=%q", tr.Tokens[0].Text)
	}
	if tr.Tokens[1].Text != "assuntos." {
		t.Fatalf("second=%q", tr.Tokens[1].Text)
	}
}
