package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"cenarius-autocut/internal/config"
	"cenarius-autocut/internal/execx"
)

type Token struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	P     float64 `json:"p,omitempty"`
}
type Segment struct {
	Text   string  `json:"text"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Tokens []Token `json:"tokens,omitempty"`
}
type Transcript struct {
	Language string    `json:"language"`
	Segments []Segment `json:"segments"`
	Tokens   []Token   `json:"tokens"`
}

type raw struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text   string `json:"text"`
		Tokens []struct {
			Text    string `json:"text"`
			Offsets *struct {
				From int64 `json:"from"`
				To   int64 `json:"to"`
			} `json:"offsets,omitempty"`
			P float64 `json:"p"`
		} `json:"tokens"`
	} `json:"transcription"`
}

func WhisperCPP(ctx context.Context, cfg config.WhisperConfig, wav, outDir string) (Transcript, string, error) {
	if err := execx.LookPath(cfg.Binary); err != nil {
		return Transcript{}, "", err
	}
	if cfg.Model == "" {
		return Transcript{}, "", fmt.Errorf("whisper.model não configurado")
	}
	if _, err := os.Stat(cfg.Model); err != nil {
		return Transcript{}, "", fmt.Errorf("modelo Whisper não encontrado em %s", cfg.Model)
	}
	prefix := filepath.Join(outDir, "transcript")

	// Important: do NOT use -ml 1 here. -ml is maximum segment length in
	// characters; setting it to 1 destroys sentence context and produces the
	// one-word/fragmented transcript that looked bad in the first prototype.
	args := []string{"-m", cfg.Model, "-f", wav, "-l", cfg.Language, "-ojf", "-of", prefix, "-sow", "-np", "-tp", "0", "-bs", "5"}
	if strings.TrimSpace(cfg.Prompt) != "" {
		args = append(args, "--prompt", cfg.Prompt, "--carry-initial-prompt")
	}
	if cfg.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(cfg.Threads))
	} else if runtime.NumCPU() > 2 {
		args = append(args, "-t", strconv.Itoa(runtime.NumCPU()-1))
	}
	if _, err := execx.Run(ctx, nil, cfg.Binary, args...); err != nil {
		return Transcript{}, "", err
	}
	p := prefix + ".json"
	b, err := os.ReadFile(p)
	if err != nil {
		return Transcript{}, "", err
	}
	tr, err := ParseJSON(b)
	return tr, p, err
}

func ParseJSON(b []byte) (Transcript, error) {
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return Transcript{}, err
	}
	out := Transcript{Language: r.Result.Language}
	for _, s := range r.Transcription {
		seg := Segment{Text: strings.TrimSpace(s.Text), Start: float64(s.Offsets.From) / 1000, End: float64(s.Offsets.To) / 1000}
		seg.Tokens = mergeWhisperPieces(s.Tokens, seg.Start, seg.End)
		if len(seg.Tokens) > 0 {
			seg.Text = tokensText(seg.Tokens)
			out.Tokens = append(out.Tokens, seg.Tokens...)
		}
		if seg.Text != "" || len(seg.Tokens) > 0 {
			out.Segments = append(out.Segments, seg)
		}
	}

	// If tokens were absent or lacked useful timings, derive word timings from segments.
	if len(out.Tokens) == 0 || !usefulTokens(out.Tokens) {
		out.Tokens = nil
		for si := range out.Segments {
			s := &out.Segments[si]
			words := strings.Fields(s.Text)
			s.Tokens = nil
			if len(words) == 0 {
				continue
			}
			step := (s.End - s.Start) / float64(len(words))
			for i, w := range words {
				t := Token{Text: w, Start: s.Start + float64(i)*step, End: s.Start + float64(i+1)*step}
				s.Tokens = append(s.Tokens, t)
				out.Tokens = append(out.Tokens, t)
			}
		}
	}
	return out, nil
}

// mergeWhisperPieces converts Whisper's BPE token pieces into real words.
// Examples: " test" + "ando" => "testando" and " ass" + "unt" + "os" => "assuntos".
// The old code trimmed every piece and treated it as a word, which is why
// captions showed artifacts such as "TEST ANDO" and "ASS UNT".
func mergeWhisperPieces(in []struct {
	Text    string `json:"text"`
	Offsets *struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	} `json:"offsets,omitempty"`
	P float64 `json:"p"`
}, segStart, segEnd float64) []Token {
	var out []Token
	var cur *Token
	var probSum float64
	var probN int

	flush := func() {
		if cur == nil || strings.TrimSpace(cur.Text) == "" {
			cur = nil
			probSum = 0
			probN = 0
			return
		}
		cur.Text = strings.TrimSpace(cur.Text)
		if probN > 0 {
			cur.P = probSum / float64(probN)
		}
		out = append(out, *cur)
		cur = nil
		probSum = 0
		probN = 0
	}

	for _, rt := range in {
		rawText := rt.Text
		trimmed := strings.TrimSpace(rawText)
		if trimmed == "" || isSpecialToken(trimmed) {
			continue
		}
		start, end := segStart, segEnd
		if rt.Offsets != nil {
			start = float64(rt.Offsets.From) / 1000
			end = float64(rt.Offsets.To) / 1000
		}

		// Punctuation belongs to the previous word and should never become its own caption word.
		if punctuationOnly(trimmed) {
			if cur != nil {
				cur.Text += trimmed
				if end > cur.End {
					cur.End = end
				}
				probSum += rt.P
				probN++
			} else if len(out) > 0 {
				out[len(out)-1].Text += trimmed
				if end > out[len(out)-1].End {
					out[len(out)-1].End = end
				}
			}
			continue
		}

		startsNewWord := hasLeadingSpace(rawText) || cur == nil
		if startsNewWord {
			flush()
			cur = &Token{Text: trimmed, Start: start, End: end}
			probSum = rt.P
			probN = 1
			continue
		}

		// No leading whitespace means a BPE continuation of the current word.
		cur.Text += trimmed
		if end > cur.End {
			cur.End = end
		}
		probSum += rt.P
		probN++
	}
	flush()
	return out
}

func tokensText(ts []Token) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 && !punctuationOnly(t.Text) {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
	}
	return strings.TrimSpace(b.String())
}

func hasLeadingSpace(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8FirstRune(s)
	return unicode.IsSpace(r)
}

func utf8FirstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func punctuationOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPunct(r) && !unicode.IsSymbol(r) {
			return false
		}
	}
	return true
}

func isSpecialToken(s string) bool {
	return strings.HasPrefix(s, "[_") || strings.HasPrefix(s, "<|") || strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")
}

func usefulTokens(ts []Token) bool {
	for _, t := range ts {
		if t.End-t.Start > 0.005 && t.Start >= 0 {
			return true
		}
	}
	return false
}
