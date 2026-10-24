package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/cmrigney/sbx-projects/internal/core"
)

// maxUpload caps the files sent with one message. It is a variable so
// tests need not send that much.
var maxUpload int64 = 100 << 20

// maxText caps a message's text, as readJSON caps a JSON message.
const maxText = 1 << 20

// maxUploadFiles caps how many files one message can carry.
const maxUploadFiles = 20

// upload is a message's text and the files sent with it, saved in a
// directory on the host until they are copied into the sandbox.
type upload struct {
	text  string
	dir   string
	files []string
	id    string
}

// readUpload reads a multipart message: a "text" field and any number of
// "file" parts. Files are written to a new directory as they arrive, so a
// large one is never held in memory.
func readUpload(w http.ResponseWriter, r *http.Request) (*upload, error) {
	// The text and the form's framing get room beyond the files.
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+maxText+64<<10)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fail(http.StatusBadRequest, "The request isn't a valid form: %s", err)
	}
	dir, err := os.MkdirTemp("", "sbx-projects-upload-")
	if err != nil {
		return nil, err
	}
	up := &upload{dir: dir, id: uploadID()}
	taken := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			up.remove()
			return nil, uploadErr(err)
		}
		switch part.FormName() {
		case "text":
			b, err := io.ReadAll(io.LimitReader(part, maxText+1))
			if err != nil {
				up.remove()
				return nil, uploadErr(err)
			}
			if len(b) > maxText {
				up.remove()
				return nil, fail(http.StatusRequestEntityTooLarge, "The message is too long. Keep it under %d KB", maxText>>10)
			}
			up.text = string(b)
		case "file":
			if len(up.files) == maxUploadFiles {
				up.remove()
				return nil, fail(http.StatusRequestEntityTooLarge, "Attach at most %d files to a message", maxUploadFiles)
			}
			name := uniqueName(safeName(part.FileName()), taken)
			p := filepath.Join(dir, name)
			if err := saveFile(p, part); err != nil {
				up.remove()
				return nil, uploadErr(err)
			}
			up.files = append(up.files, p)
		}
		part.Close()
	}
	return up, nil
}

func saveFile(p string, src io.Reader) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func uploadErr(err error) error {
	if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
		return fail(http.StatusRequestEntityTooLarge, "The files are too large. Attach up to %d MB at a time", maxUpload>>20)
	}
	return fail(http.StatusBadRequest, "Could not read the files: %s", err)
}

// sandboxDir is where the files go in the sandbox, a directory of their
// own so files with the same name sent in different messages both stay.
func (u *upload) sandboxDir() string {
	return path.Join(core.AttachDir, u.id)
}

// sandboxPaths are the files' paths in the sandbox, in the order they came.
func (u *upload) sandboxPaths(dir string) []string {
	var out []string
	for _, f := range u.files {
		out = append(out, path.Join(dir, filepath.Base(f)))
	}
	return out
}

// remove deletes the host's copy of the files.
func (u *upload) remove() {
	if u.dir == "" {
		return
	}
	if err := os.RemoveAll(u.dir); err != nil {
		slog.Warn("remove uploaded files", "dir", u.dir, "err", err)
	}
}

// uploadID names a message's directory of files, sorting by when it came.
func uploadID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// The web and iPhone apps name files with the same rules as safeName and
// uniqueName before they show them, so the name a message mentions is the
// one the file has in the sandbox. Keep web/src/names.ts and the iPhone
// app's Attachment in step; testdata/names.json holds the cases all three
// are tested against.

// maxName is the longest name, in code points, a file is given.
const maxName = 120

// safeName keeps the last element of what the client called the file and
// replaces anything a shell or the agent's prompt could trip on, so the
// path can be read back out of the message as it is. A name it returns
// comes back from it unchanged.
func safeName(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '.' || r == '-' || r == '_':
			return r
		case unicode.IsLetter(r) || unicode.Is(unicode.Nd, r):
			return r
		}
		return '_'
	}, name)
	name = strings.TrimLeft(name, ".")
	if r := []rune(name); len(r) > maxName {
		ext := []rune(path.Ext(name))
		if len(ext) > 20 {
			ext = nil
		}
		name = string(r[:maxName-len(ext)]) + string(ext)
	}
	if strings.Trim(name, "_") == "" {
		return "file"
	}
	return name
}

// uniqueName numbers a name already used by an earlier file in the
// message, shortening it to keep within maxName.
func uniqueName(name string, taken map[string]bool) string {
	ext := path.Ext(name)
	base := []rune(strings.TrimSuffix(name, ext))
	out := name
	for i := 2; taken[out]; i++ {
		suffix := fmt.Sprintf("-%d%s", i, ext)
		b := base
		if keep := maxName - len([]rune(suffix)); len(b) > keep {
			b = b[:max(keep, 0)]
		}
		out = string(b) + suffix
	}
	taken[out] = true
	return out
}

func countFiles(n int) string {
	if n == 1 {
		return "a file"
	}
	return fmt.Sprintf("%d files", n)
}
