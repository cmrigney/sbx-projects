package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Device is a phone paired with this Mac. Only a hash of its token is
// kept, so devices.json can't be used to sign in.
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TokenHash  string `json:"tokenHash"`
	PairedAt   int64  `json:"pairedAt"`
	LastSeenAt int64  `json:"lastSeenAt,omitempty"`
}

// pairCodeAlphabet is Crockford's base32 without the letters people
// misread, so a code can be typed from the screen.
const pairCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// maxPairTries closes the pairing window after this many wrong codes, which
// keeps a 40-bit code out of reach of guessing.
const maxPairTries = 5

var (
	ErrNotPairing = errors.New("pairing isn't open; run sbx-projects api --pair on the Mac")
	ErrWrongCode  = errors.New("that pairing code is wrong")
)

// Devices is the list of paired devices in devices.json, and the pairing
// window when one is open.
type Devices struct {
	path string

	mu   sync.Mutex
	list []Device
	// saved is when LastSeenAt was last written, so a busy client doesn't
	// rewrite the file on every request.
	saved time.Time
	// mtime is the file's modification time when it was last read or
	// written, so a device removed by another process is noticed.
	mtime time.Time

	code     string
	until    time.Time
	tries    int
	onPaired func(Device)
}

func LoadDevices(path string) (*Devices, error) {
	d := &Devices{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return d, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &d.list); err != nil {
		return nil, err
	}
	d.mtime = modTime(path)
	return d, nil
}

// reload rereads the file when something else changed it, as
// sbx-projects api devices rm does. Callers hold d.mu.
func (d *Devices) reload() {
	mt := modTime(d.path)
	if mt.IsZero() || mt.Equal(d.mtime) {
		return
	}
	b, err := os.ReadFile(d.path)
	if err != nil {
		return
	}
	var list []Device
	if json.Unmarshal(b, &list) == nil {
		d.list, d.mtime = list, mt
	}
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func (d *Devices) List() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Device(nil), d.list...)
}

// OpenPairing starts a pairing window and returns its code. onPaired, when
// set, is called after a device pairs, which closes the window.
func (d *Devices) OpenPairing(ttl time.Duration, onPaired func(Device)) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.code, d.until, d.tries, d.onPaired = randomCode(8), time.Now().Add(ttl), 0, onPaired
	return d.code
}

// Pairing says whether a pairing window is open.
func (d *Devices) Pairing() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.code != "" && time.Now().Before(d.until)
}

// Pair trades the pairing code for a new device's token.
func (d *Devices) Pair(code, name string) (Device, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.code == "" || time.Now().After(d.until) {
		return Device{}, "", ErrNotPairing
	}
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if subtle.ConstantTimeCompare([]byte(code), []byte(d.code)) != 1 {
		d.tries++
		if d.tries >= maxPairTries {
			d.code = ""
		}
		return Device{}, "", ErrWrongCode
	}
	d.code = ""
	d.reload()
	name = strings.TrimSpace(name)
	if name == "" {
		name = "iPhone"
	}
	token := randomHex(32)
	dev := Device{ID: randomHex(4), Name: name, TokenHash: hashToken(token), PairedAt: time.Now().Unix()}
	d.list = append(d.list, dev)
	if err := d.save(); err != nil {
		d.list = d.list[:len(d.list)-1]
		return Device{}, "", err
	}
	if d.onPaired != nil {
		go d.onPaired(dev)
	}
	return dev, token, nil
}

// Authenticate finds the device a token belongs to.
func (d *Devices) Authenticate(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	h := hashToken(token)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reload()
	for i := range d.list {
		if subtle.ConstantTimeCompare([]byte(d.list[i].TokenHash), []byte(h)) == 1 {
			now := time.Now()
			d.list[i].LastSeenAt = now.Unix()
			if now.Sub(d.saved) > time.Minute {
				_ = d.save()
			}
			return d.list[i], true
		}
	}
	return Device{}, false
}

// Has says whether the device is still paired, noticing a removal by
// another process.
func (d *Devices) Has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reload()
	for _, dev := range d.list {
		if dev.ID == id {
			return true
		}
	}
	return false
}

// Remove forgets the device with the given ID or name, signing it out.
func (d *Devices) Remove(idOrName string) (Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reload()
	for i, dev := range d.list {
		if dev.ID == idOrName || strings.EqualFold(dev.Name, idOrName) {
			d.list = append(d.list[:i:i], d.list[i+1:]...)
			return dev, d.save()
		}
	}
	return Device{}, errors.New("no paired device is called " + idOrName)
}

// save writes the list so a crash never leaves half a file. Callers hold
// d.mu.
func (d *Devices) save() error {
	d.saved = time.Now()
	b, err := json.MarshalIndent(d.list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return err
	}
	d.mtime = modTime(d.path)
	return nil
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomCode(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairCodeAlphabet[int(b[i])%len(pairCodeAlphabet)]
	}
	return string(b)
}
