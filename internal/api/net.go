package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LoadOrCreateCert returns the server's TLS certificate from dir, making
// one the first time. Phones pin its SHA-256 fingerprint when they pair,
// so the certificate is kept rather than renewed, and names no addresses:
// the Mac's address on the LAN changes, and clients check the pin instead.
func LoadOrCreateCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, Fingerprint(c.Certificate[0]), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", fmt.Errorf("load %s: %w", certPath, err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "sbx-projects on " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return c, Fingerprint(der), nil
}

// Fingerprint is the lowercase hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// FormatFingerprint groups a fingerprint in pairs, the way people compare
// them by eye.
func FormatFingerprint(fp string) string {
	var parts []string
	for i := 0; i+2 <= len(fp); i += 2 {
		parts = append(parts, strings.ToUpper(fp[i:i+2]))
	}
	return strings.Join(parts, ":")
}

// skipInterface names interfaces that aren't the LAN: VMs, containers and
// VPN tunnels.
var skipInterface = []string{"docker", "br-", "veth", "vmnet", "bridge", "utun", "awdl", "llw", "vboxnet", "virbr", "tailscale", "anpi", "ap"}

// LANAddrs lists the IPv4 addresses a phone on the same network can reach,
// Wi-Fi and Ethernet (en*) first.
func LANAddrs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	type addr struct {
		ip    string
		first bool
	}
	var out []addr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || skippedInterface(ifc.Name) {
			continue
		}
		as, _ := ifc.Addrs()
		for _, a := range as {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := n.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			out = append(out, addr{ip.String(), strings.HasPrefix(ifc.Name, "en") || strings.HasPrefix(ifc.Name, "eth")})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].first && !out[j].first })
	ips := make([]string, len(out))
	for i, a := range out {
		ips[i] = a.ip
	}
	return ips
}

func skippedInterface(name string) bool {
	for _, p := range skipInterface {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ServiceType is the Bonjour service the app browses for.
const ServiceType = "_sbxp._tcp"

// Advertise announces the API over Bonjour until ctx is done, so the app
// finds the Mac again when its address changes. It uses the system's own
// responder: dns-sd on macOS, avahi-publish-service on Linux. txt entries
// are "key=value".
func Advertise(ctx context.Context, name string, port int, txt []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.CommandContext(ctx, "dns-sd", append([]string{"-R", name, ServiceType, "local", strconv.Itoa(port)}, txt...)...)
	default:
		bin, err := exec.LookPath("avahi-publish-service")
		if err != nil {
			return nil, errors.New("avahi-publish-service isn't installed, so the app can't find this machine by itself")
		}
		cmd = exec.CommandContext(ctx, bin, append([]string{name, ServiceType, strconv.Itoa(port)}, txt...)...)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd, nil
}
