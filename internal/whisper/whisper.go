// Package whisper turns speech from the Wio Terminal's microphone into text
// with whisper.cpp, on this machine. Audio never leaves it.
package whisper

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// SampleRate is the rate of the audio, the one Whisper expects.
	SampleRate = 16000

	// ModelFile is the default model: English, 142 MB, and quick on a Mac.
	ModelFile = "ggml-base.en.bin"
	modelURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + ModelFile
	modelSHA1 = "137c40403d78fd54d454da0f9bd998f78703390c"

	// maxText bounds what is typed into a terminal from one recording.
	maxText = 1000
)

// These are short enough to show on the Wio's status bar.
var (
	ErrNotSetUp = errors.New("Run: sbx-projects api whisper")
	ErrNoSpeech = errors.New("Didn't catch that")
)

// Whisper runs whisper-cli, one recording at a time.
type Whisper struct {
	Bin   string
	Model string
	slot  chan struct{}
}

func New(bin, model string) *Whisper {
	return &Whisper{Bin: bin, Model: model, slot: make(chan struct{}, 1)}
}

// binary finds whisper-cli, including in Homebrew's folders, which a server
// started outside a login shell may not have on its PATH.
func (w *Whisper) binary() (string, error) {
	if p, err := exec.LookPath(w.Bin); err == nil {
		return p, nil
	}
	if !strings.ContainsRune(w.Bin, filepath.Separator) {
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
			if p, err := exec.LookPath(filepath.Join(dir, w.Bin)); err == nil {
				return p, nil
			}
		}
	}
	return "", ErrNotSetUp
}

// Ready reports whether whisper-cli and the model are both in place.
func (w *Whisper) Ready() bool {
	if _, err := w.binary(); err != nil {
		return false
	}
	info, err := os.Stat(w.Model)
	return err == nil && info.Mode().IsRegular()
}

// Transcribe returns the words in a recording of IMA ADPCM audio.
func (w *Whisper) Transcribe(ctx context.Context, adpcm []byte) (string, error) {
	bin, err := w.binary()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(w.Model); err != nil {
		return "", ErrNotSetUp
	}
	select {
	case w.slot <- struct{}{}:
		defer func() { <-w.slot }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	f, err := os.CreateTemp("", "sbx-dictation-*.wav") // private to this user
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(WAV(Clean(DecodeADPCM(adpcm))))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-m", w.Model, "-f", f.Name(), "-l", "en", "-nt", "-np")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("whisper-cli: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	text := CleanText(stdout.String())
	if text == "" {
		return "", ErrNoSpeech
	}
	return text, nil
}

// Whisper marks sounds that aren't words on lines of their own, like
// [BLANK_AUDIO] or (wind blowing).
var nonSpeech = regexp.MustCompile(`^(\[[^\]]*\]|\([^)]*\)|\*[^*]*\*)$`)

// Whisper writes a spoken symbol out as words.
var (
	quotedSlash = regexp.MustCompile(`(?i)\b(?:open[\s-]*)?quote[,.:]?\s+forward[\s-]*slash[,.]?\s+(?:(?:end|close)[\s-]*quote|unquote)\b`)
	spokenSlash = regexp.MustCompile(`(?i)\s*\bforward[\s-]*slash\b(?:[.,!?;:](?:\s|$))?\s*`)
)

// translateSymbols turns "forward slash" into /, joining the words on either
// side, so "src forward slash main" is src/main. Text that's a single word
// with a slash, like /usage. or src/main., loses the punctuation Whisper adds,
// which would stop it working as a slash command or path. Whisper sometimes
// writes the slash itself, so this doesn't depend on a translation.
func translateSymbols(text string) string {
	out := quotedSlash.ReplaceAllString(text, `"/"`)
	out = spokenSlash.ReplaceAllString(out, "/")
	if strings.Contains(out, "/") && !strings.Contains(out, " ") {
		out = strings.TrimRight(out, ".,!?;:")
	}
	return out
}

// CleanText makes Whisper's output safe to type into a terminal: one line,
// with no control characters, so it can't press Enter or send an escape
// sequence, and of a bounded length.
func CleanText(out string) string {
	var words []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !nonSpeech.MatchString(line) {
			words = append(words, line)
		}
	}
	joined := strings.ToValidUTF8(strings.Join(words, " "), "")
	joined = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, joined)
	joined = translateSymbols(strings.Join(strings.Fields(joined), " "))
	if len(joined) > maxText {
		cut := maxText
		for cut > 0 && !utf8.RuneStart(joined[cut]) {
			cut--
		}
		joined = joined[:cut]
		if i := strings.LastIndexByte(joined, ' '); i > 0 {
			joined = joined[:i]
		}
	}
	return joined
}

// ---------------------------------------------------------------------------
// Audio
// ---------------------------------------------------------------------------

var adpcmSteps = [89]int32{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45, 50, 55, 60, 66, 73, 80, 88, 97,
	107, 118, 130, 143, 157, 173, 190, 209, 230, 253, 279, 307, 337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
	876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066, 2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871,
	5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899, 15289, 16818, 18500, 20350, 22385, 24623,
	27086, 29794, 32767,
}

var adpcmIndex = [8]int{-1, -1, -1, -1, 2, 4, 6, 8}

// DecodeADPCM decodes IMA ADPCM, two samples a byte, low nibble first, from a
// predictor and step index of zero.
func DecodeADPCM(in []byte) []int16 {
	out := make([]int16, 0, len(in)*2)
	var predicted int32
	index := 0
	decode := func(code byte) {
		step := adpcmSteps[index]
		diff := step >> 3
		if code&4 != 0 {
			diff += step
		}
		if code&2 != 0 {
			diff += step >> 1
		}
		if code&1 != 0 {
			diff += step >> 2
		}
		if code&8 != 0 {
			diff = -diff
		}
		predicted = max(-32768, min(32767, predicted+diff))
		index = max(0, min(88, index+adpcmIndex[code&7]))
		out = append(out, int16(predicted))
	}
	for _, b := range in {
		decode(b & 0x0F)
		decode(b >> 4)
	}
	return out
}

// Clean removes any DC offset and brings quiet recordings up to a level
// Whisper hears well. The Wio's microphone is quiet and has no gain control.
func Clean(pcm []int16) []int16 {
	if len(pcm) == 0 {
		return nil
	}
	out := make([]int16, len(pcm))
	var sum int64
	for _, s := range pcm {
		sum += int64(s)
	}
	mean := sum / int64(len(pcm))
	var peak int64
	for _, s := range pcm {
		peak = max(peak, abs(int64(s)-mean))
	}
	const target, maxGain = 24000, 40
	gain := float64(maxGain)
	if peak > 0 {
		gain = min(gain, float64(target)/float64(peak))
	}
	for i, s := range pcm {
		v := float64(int64(s)-mean) * gain
		out[i] = int16(max(-32768, min(32767, v)))
	}
	return out
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// WAV wraps 16 kHz mono samples in a WAV file.
func WAV(pcm []int16) []byte {
	var b bytes.Buffer
	size := uint32(len(pcm) * 2)
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	put(36 + size)
	b.WriteString("WAVEfmt ")
	put(uint32(16))
	put(uint16(1)) // PCM
	put(uint16(1)) // mono
	put(uint32(SampleRate))
	put(uint32(SampleRate * 2))
	put(uint16(2))
	put(uint16(16))
	b.WriteString("data")
	put(size)
	put(pcm)
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// Setup checks for whisper-cli and downloads the model if it's missing,
// verifying its checksum before putting it in place.
func Setup(ctx context.Context, w *Whisper, out io.Writer) error {
	bin, err := w.binary()
	if err != nil {
		return fmt.Errorf("%s isn't installed. Install it with: brew install whisper-cpp", w.Bin)
	}
	fmt.Fprintf(out, "whisper-cli: %s\n", bin)
	if _, err := os.Stat(w.Model); err == nil {
		fmt.Fprintf(out, "Model: %s\nDictation is ready. Restart sbx-projects api to use it.\n", w.Model)
		return nil
	}
	if filepath.Base(w.Model) != ModelFile {
		return fmt.Errorf("there's no model at %s", w.Model)
	}
	if err := os.MkdirAll(filepath.Dir(w.Model), 0o700); err != nil {
		return err
	}
	fmt.Fprintf(out, "Downloading %s (142 MB)…\n", ModelFile)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading the model: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.Model), ModelFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	sum := sha1.New()
	_, err = io.Copy(io.MultiWriter(tmp, sum), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading the model: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != modelSHA1 {
		return fmt.Errorf("the downloaded model is corrupt (SHA-1 %s)", got)
	}
	if err := os.Rename(tmp.Name(), w.Model); err != nil {
		return err
	}
	fmt.Fprintf(out, "Model: %s\nDictation is ready. Restart sbx-projects api to use it.\n", w.Model)
	return nil
}
