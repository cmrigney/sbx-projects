package whisper

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestCleanTextCannotControlTheTerminal(t *testing.T) {
	for in, want := range map[string]string{
		" Hello there.\n And more.\n":     "Hello there. And more.",
		"[BLANK_AUDIO]\n":                 "",
		" (wind blowing)\n real words\n":  "real words",
		"run\x1b[31m this\r\nnow\x7f\x00": "run[31m this now",
		"a\u202eb\u0085c":                 "ab c",
		"keep [these] words":              "keep [these] words",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
	long := CleanText(strings.Repeat("héllo ", 400))
	if len(long) > maxText || strings.HasSuffix(long, " ") || !strings.HasSuffix(long, "héllo") {
		t.Errorf("long text was cut badly: %d bytes ending %q", len(long), long[len(long)-8:])
	}
}

func TestDecodeADPCM(t *testing.T) {
	// Code 7 is the largest rise and code 15 the largest fall.
	got := DecodeADPCM([]byte{0x77, 0xFF})
	want := []int16{11, 41, -22, -158}
	if len(got) != len(want) {
		t.Fatalf("got %d samples", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCleanCentersAndAmplifies(t *testing.T) {
	out := Clean([]int16{2000, 0, 2000, 0})
	if out[0] != 24000 || out[1] != -24000 {
		t.Fatalf("got %v", out)
	}
	if Clean(nil) != nil {
		t.Fatal("empty audio should stay empty")
	}
}

func TestWAVHeader(t *testing.T) {
	b := WAV([]int16{1, 2, 3})
	if len(b) != 50 || string(b[:4]) != "RIFF" || string(b[36:40]) != "data" {
		t.Fatalf("bad header: %d bytes", len(b))
	}
	if binary.LittleEndian.Uint32(b[24:]) != SampleRate || binary.LittleEndian.Uint32(b[40:]) != 6 {
		t.Fatal("wrong rate or data size")
	}
}

func TestCleanTextTranslatesForwardSlash(t *testing.T) {
	for in, want := range map[string]string{
		"Forward slash.":                           "/",
		"Forward slash compact.":                   "/compact",
		"src forward slash main forward slash app": "src/main/app",
		"Look in src forward-slash main.":          "Look in src/main.",
		`Type "forward slash" now.`:                `Type "/" now.`,
		"Type quote forward slash end quote now":   `Type "/" now`,
		"Quote, forward slash, end quote":          `"/"`,
		"quote forward slash unquote":              `"/"`,
		"Move forward. Slash the budget.":          "Move forward. Slash the budget.",
		"a forwardslasher":                         "a forwardslasher",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}
