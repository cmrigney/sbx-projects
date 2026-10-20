package sbx

import (
	"context"
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
