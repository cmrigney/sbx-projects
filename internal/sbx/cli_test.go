package sbx

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreatePassesKitsAndMCPs(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "sbx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := NewCLI(bin).Create(context.Background(), CreateOptions{Name: "n", Agent: "claude", Path: "/src", Clone: true, Kits: []string{"./a", "ghcr.io/x/b:1"}, MCPs: []string{"notion", "github"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argv)
	want := "run -d --name n --clone --kit ./a --kit ghcr.io/x/b:1 --static-mcp notion,github claude /src"
	if got := strings.Join(strings.Fields(string(b)), " "); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestMCPServersReadsTheJSONListing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sbx")
	out := `[{"name":"notion","transport":"streamable-http","status":"ready","gateway":{"name":"local","local":true}},` +
		`{"name":"linear","status":"needs-auth","next_step":"sbx mcp auth linear"}]`
	script := "#!/bin/sh\n[ \"$*\" = \"mcp ls --json\" ] || exit 1\ncat <<'EOF'\n" + out + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := NewCLI(bin).MCPServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "notion" || !list[0].Ready() || list[1].Ready() || list[1].NextStep != "sbx mcp auth linear" {
		t.Fatalf("list = %+v", list)
	}
}

func TestFailedCallLogsAllOfStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sbx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho first problem >&2\necho last problem >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, err := NewCLI(bin).Exec(context.Background(), "box", "secret prompt")
	if err == nil || !strings.Contains(err.Error(), "last problem") {
		t.Fatalf("err = %v", err)
	}
	got := log.String()
	for _, want := range []string{"sbx failed", "first problem", "last problem", "exit status 3", "stdin_bytes=13"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret prompt") {
		t.Errorf("log has the script:\n%s", got)
	}
}

func TestParsePortRoundTrips(t *testing.T) {
	for spec, want := range map[string]Port{
		"3000":                    {SandboxPort: 3000},
		"8080:3000":               {HostPort: 8080, SandboxPort: 3000},
		"127.0.0.1:8080:3000/udp": {HostIP: "127.0.0.1", HostPort: 8080, SandboxPort: 3000, Protocol: "udp"},
		"[::1]:8080:3000/tcp6":    {HostIP: "::1", HostPort: 8080, SandboxPort: 3000, Protocol: "tcp6"},
	} {
		got, err := ParsePort(spec)
		if err != nil || got != want {
			t.Errorf("ParsePort(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
		if got.Spec() != spec {
			t.Errorf("Spec() = %q, want %q", got.Spec(), spec)
		}
	}
	for _, bad := range []string{"", "x", "0", "70000", "1:2:3:4", "8080:3000/sctp", "[::1]:3000", "[::1:8080:3000"} {
		if p, err := ParsePort(bad); err == nil {
			t.Errorf("ParsePort(%q) = %+v, want an error", bad, p)
		}
	}
}

func TestPortsPassTheSpec(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "sbx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewCLI(bin)
	p := Port{HostPort: 8080, SandboxPort: 3000, Protocol: "tcp"}
	if err := c.PublishPort(context.Background(), "box", p); err != nil {
		t.Fatal(err)
	}
	if err := c.UnpublishPort(context.Background(), "box", p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argv)
	want := "ports box --publish 8080:3000/tcp ports box --unpublish 8080:3000/tcp"
	if got := strings.Join(strings.Fields(string(b)), " "); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}
