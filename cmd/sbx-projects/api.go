package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rsc.io/qr"

	"github.com/cmrigney/sbx-projects/internal/api"
	"github.com/cmrigney/sbx-projects/internal/applog"
	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/store"
	"github.com/cmrigney/sbx-projects/internal/webui"
)

const pairFor = 5 * time.Minute

const apiUsage = `Usage:
  sbx-projects api [flags]           serve the API to the iOS app and the web app
  sbx-projects api devices           list paired devices
  sbx-projects api devices rm NAME   sign a device out

Flags:
`

func runAPICmd(args []string) error {
	defPath, _ := store.DefaultPath()
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), apiUsage); fs.PrintDefaults() }
	dbPath := fs.String("db", defPath, "path to the projects database")
	sbxBin := fs.String("sbx", "sbx", "sbx binary to run")
	demo := fs.Bool("demo", false, "serve built-in demo data instead of sbx")
	listen := fs.String("listen", ":7443", "address to listen on")
	pair := fs.Bool("pair", false, "show a code to pair a new device")
	host, _ := os.Hostname()
	name := fs.String("name", strings.TrimSuffix(host, ".local"), "name the app shows for this machine")
	logPath := fs.String("log", os.Getenv("SBX_PROJECTS_LOG"), `debug log file; empty for a new one in the temp directory, "off" for none`)
	logLevel := slog.LevelInfo
	fs.TextVar(&logLevel, "log-level", logLevel, "debug log level: debug, info, warn or error")
	_ = fs.Parse(args)

	// Paired devices and the certificate outlive --demo's in-memory data.
	dir := filepath.Join(filepath.Dir(*dbPath), "api")
	devices, err := api.LoadDevices(filepath.Join(dir, "devices.json"))
	if err != nil {
		return err
	}
	if fs.Arg(0) == "devices" {
		return devicesCmd(devices, fs.Args()[1:])
	}
	if fs.NArg() > 0 {
		fs.Usage()
		os.Exit(2)
	}

	path, closer, err := applog.Open(*logPath, logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbx-projects api: no debug log:", err)
	}
	defer closer.Close()
	slog.Info("starting api", "args", args, "db", *dbPath, "sbx", *sbxBin, "demo", *demo, "listen", *listen, "os", runtime.GOOS, "arch", runtime.GOARCH)
	if err := serveAPI(devices, dir, *dbPath, *sbxBin, *demo, *listen, *pair, *name, path); err != nil {
		slog.Error("exiting", "err", err)
		if path != "" {
			err = fmt.Errorf("%w; details in %s", err, path)
		}
		return err
	}
	slog.Info("exiting")
	return nil
}

func serveAPI(devices *api.Devices, dir, dbPath, sbxBin string, demo bool, listen string, pair bool, name, logPath string) error {
	st, be, err := openBackend(dbPath, sbxBin, demo)
	if err != nil {
		return err
	}
	defer st.Close()
	cert, fp, err := api.LoadOrCreateCert(dir)
	if err != nil {
		return err
	}
	mon, err := api.NewMonitor(&core.Service{Backend: be, Store: st, Demo: demo})
	if err != nil {
		return err
	}
	srv := &api.Server{Monitor: mon, Devices: devices, ID: fp[:16], Name: name}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go mon.Run(ctx)

	addrs := api.LANAddrs()
	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsUnspecified() {
		addrs = []string{ip.String()}
	}
	fmt.Printf("sbx-projects API for %s\n", name)
	for _, a := range addrs {
		fmt.Printf("  https://%s:%d\n", a, port)
	}
	if len(addrs) == 0 {
		fmt.Printf("  No LAN address found; listening on %s\n", ln.Addr())
	}
	fmt.Printf("  Certificate %s\n", api.FormatFingerprint(fp)[:47]+"…")
	if _, err := api.Advertise(ctx, name, port, []string{"id=" + srv.ID, "v=" + strconv.Itoa(api.Version)}); err != nil {
		fmt.Printf("  Not advertised on the network: %s\n", err)
	}
	if demo {
		fmt.Println("  Serving demo data")
	}
	if logPath != "" {
		fmt.Printf("  Debug log at %s\n", logPath)
	}

	if pair || len(devices.List()) == 0 {
		showPairing(devices, srv, fp, addrs, port)
	} else {
		fmt.Printf("\n%d paired device(s). Run with --pair to add another.\n", len(devices.List()))
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/", srv.Handler())
	mux.Handle("/", webui.Handler())
	hs := &http.Server{
		Handler:           mux,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(httpErrorLog{}, "", 0),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if err := hs.ServeTLS(ln, "", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// httpErrorLog sends the HTTP server's errors to the debug log. Clients that
// hang up before sending a request are mostly phones and browsers that
// haven't pinned this certificate: with TLS 1.3 the server sees that as a
// failed HTTP/2 preface rather than a failed handshake. Those only show at
// debug level.
type httpErrorLog struct{}

func (httpErrorLog) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	level := slog.LevelWarn
	if strings.Contains(msg, "TLS handshake error") || strings.Contains(msg, "error reading preface") {
		level = slog.LevelDebug
	}
	slog.Log(context.Background(), level, "http server", "err", msg)
	return len(p), nil
}

// showPairing opens a pairing window and prints its code, as text and as a
// QR code that carries the certificate's fingerprint so the app can pin it.
func showPairing(devices *api.Devices, srv *api.Server, fp string, addrs []string, port int) {
	paired := make(chan struct{})
	code := devices.OpenPairing(pairFor, func(d api.Device) {
		close(paired)
		fmt.Printf("✓ Paired %q\n", d.Name)
	})
	go func() {
		select {
		case <-paired:
		case <-time.After(pairFor):
			fmt.Println("The pairing code has expired. Run with --pair to pair a device.")
		}
	}()

	q := url.Values{}
	q.Set("v", strconv.Itoa(api.Version))
	q.Set("name", srv.Name)
	q.Set("fp", fp)
	q.Set("hosts", strings.Join(addrs, ","))
	q.Set("port", strconv.Itoa(port))
	q.Set("code", code)
	link := "sbxp://pair?" + q.Encode()

	fmt.Println("\nScan this with the sbx-projects app on your iPhone:")
	if c, err := qr.Encode(link, qr.L); err == nil {
		printQR(c)
	}
	fmt.Printf("Or pick %s in the app and enter %s-%s\n", srv.Name, code[:4], code[4:])
	if len(addrs) > 0 {
		fmt.Printf("Or open https://%s:%d/#/pair?code=%s in a browser\n", addrs[0], port, code)
	}
	fmt.Printf("The code works once, for the next %d minutes.\n\n", int(pairFor/time.Minute))
}

// printQR draws the code with half blocks, two modules per character, in
// black on white so it scans on dark and light terminals alike. A cell whose
// two modules match is a space on that background rather than a half block in
// the same color as its background: terminals that enforce a minimum contrast
// (VS Code, iTerm2) would otherwise recolor the glyph and smear the code.
func printQR(c *qr.Code) {
	const quiet = 2
	black := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < c.Size+quiet; y += 2 {
		b.WriteString("  ")
		for x := -quiet; x < c.Size+quiet; x++ {
			top, bottom := black(x, y), black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("\x1b[40m ")
			case !top && !bottom:
				b.WriteString("\x1b[107m ")
			case top:
				b.WriteString("\x1b[30;107m▀")
			default:
				b.WriteString("\x1b[97;40m▀")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	fmt.Print(b.String())
}

func devicesCmd(devices *api.Devices, args []string) error {
	switch {
	case len(args) == 0:
		list := devices.List()
		if len(list) == 0 {
			fmt.Println("No devices are paired. Run sbx-projects api --pair to pair one.")
		}
		for _, d := range list {
			seen := "never"
			if d.LastSeenAt > 0 {
				seen = time.Unix(d.LastSeenAt, 0).Format("Jan 2 15:04")
			}
			fmt.Printf("%s  %-24s paired %s, last seen %s\n", d.ID, d.Name, time.Unix(d.PairedAt, 0).Format("Jan 2 2006"), seen)
		}
		return nil
	case len(args) == 2 && args[0] == "rm":
		d, err := devices.Remove(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Signed out %q\n", d.Name)
		return nil
	}
	return errors.New("usage: sbx-projects api devices [rm NAME]")
}
