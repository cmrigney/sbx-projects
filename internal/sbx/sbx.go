// Package sbx manages sandboxes through the sbx CLI.
package sbx

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

type Port struct {
	HostIP      string `json:"host_ip"`
	HostPort    int    `json:"host_port"`
	SandboxPort int    `json:"sandbox_port"`
	Protocol    string `json:"protocol"`
}

type Sandbox struct {
	Name       string     `json:"name"`
	Agent      string     `json:"agent"`
	Status     string     `json:"status"`
	Workspaces []string   `json:"workspaces"`
	CPUs       int        `json:"cpus"`
	MemoryMiB  int64      `json:"memory_mib"`
	Ports      []Port     `json:"ports"`
	CreatedAt  *time.Time `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Detached   bool       `json:"detached"`
}

func (s Sandbox) Running() bool { return s.Status == "running" }

// Workspace is the primary workspace path without any ":ro" annotation.
func (s Sandbox) Workspace() string {
	if len(s.Workspaces) == 0 {
		return ""
	}
	return strings.TrimSuffix(s.Workspaces[0], ":ro")
}

type CreateOptions struct {
	Name  string
	Agent string
	Path  string
	Clone bool
	// Kits are mixin kit references, each passed as --kit.
	Kits []string
	// MCPs are registered MCP server names, passed as --static-mcp. sbx
	// fixes the set at creation.
	MCPs []string
}

// MCPServer is one server registered with sbx mcp add.
type MCPServer struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	// Status is "ready" when the server can connect now, otherwise what is
	// in the way, such as "needs-auth" or "needs-secret".
	Status string `json:"status"`
	Detail string `json:"detail"`
	// NextStep is the command that fixes a server that isn't ready.
	NextStep string `json:"next_step"`
}

func (s MCPServer) Ready() bool { return s.Status == "ready" }

// Backend is the subset of sbx the TUI needs. Exec must only be called on
// running sandboxes: sbx exec starts a stopped sandbox as a side effect.
type Backend interface {
	List(ctx context.Context) ([]Sandbox, error)
	// MCPServers lists the MCP servers a new sandbox can be given.
	MCPServers(ctx context.Context) ([]MCPServer, error)
	// Create makes a detached sandbox that keeps running with nothing attached.
	Create(ctx context.Context, o CreateOptions) error
	// Start starts an existing sandbox in detached mode.
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	// Exec runs a bash script in the sandbox's workspace and returns stdout.
	Exec(ctx context.Context, name, script string) (string, error)
	// Interactive returns a command that takes over the terminal.
	Interactive(name string, argv ...string) *exec.Cmd
}
