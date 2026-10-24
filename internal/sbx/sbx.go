// Package sbx manages sandboxes through the sbx CLI.
package sbx

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Port struct {
	HostIP      string `json:"host_ip"`
	HostPort    int    `json:"host_port"`
	SandboxPort int    `json:"sandbox_port"`
	Protocol    string `json:"protocol"`
}

// Protocols are the protocols sbx ports accepts. A port without one is
// tcp4.
var Protocols = []string{"tcp4", "tcp", "tcp6", "udp", "udp4", "udp6"}

// ParsePort reads sbx ports' [[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]
// form. A bracketed host IP may be IPv6. Parts left out are zero.
func ParsePort(spec string) (Port, error) {
	var p Port
	rest := strings.TrimSpace(spec)
	if i := strings.LastIndexByte(rest, '/'); i >= 0 {
		rest, p.Protocol = rest[:i], strings.ToLower(rest[i+1:])
		if !slices.Contains(Protocols, p.Protocol) {
			return Port{}, fmt.Errorf("%q isn't a protocol; use one of %s", p.Protocol, strings.Join(Protocols, ", "))
		}
	}
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]:")
		if end < 0 {
			return Port{}, fmt.Errorf("%q has an unclosed host IP", spec)
		}
		p.HostIP, rest = rest[1:end], rest[end+2:]
	}
	parts := strings.Split(rest, ":")
	if p.HostIP == "" && len(parts) == 3 {
		p.HostIP, parts = parts[0], parts[1:]
	}
	if len(parts) > 2 || (p.HostIP != "" && len(parts) != 2) {
		return Port{}, fmt.Errorf("%q isn't [[HOST_IP:]HOST_PORT:]SANDBOX_PORT[/PROTOCOL]", spec)
	}
	var err error
	if p.SandboxPort, err = portNumber(parts[len(parts)-1]); err != nil {
		return Port{}, err
	}
	if len(parts) == 2 {
		if p.HostPort, err = portNumber(parts[0]); err != nil {
			return Port{}, err
		}
	}
	return p, nil
}

func portNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q isn't a port number from 1 to 65535", s)
	}
	return n, nil
}

// Spec is the port in the form sbx ports takes, leaving out what is unset.
// A host IP without a host port uses the sandbox's port on the host.
func (p Port) Spec() string {
	s := strconv.Itoa(p.SandboxPort)
	if p.HostPort == 0 && p.HostIP != "" {
		p.HostPort = p.SandboxPort
	}
	if p.HostPort > 0 {
		s = strconv.Itoa(p.HostPort) + ":" + s
		if ip := p.HostIP; ip != "" {
			if strings.Contains(ip, ":") {
				ip = "[" + ip + "]"
			}
			s = ip + ":" + s
		}
	}
	if p.Protocol != "" {
		s += "/" + p.Protocol
	}
	return s
}

// String shows the port as host → sandbox.
func (p Port) String() string {
	host := strconv.Itoa(p.HostPort)
	if p.HostIP != "" {
		ip := p.HostIP
		if strings.Contains(ip, ":") {
			ip = "[" + ip + "]"
		}
		host = ip + ":" + host
	}
	s := fmt.Sprintf("%s → %d", host, p.SandboxPort)
	if p.Protocol != "" {
		s += "/" + p.Protocol
	}
	return s
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
	// PublishPort forwards a port on the host to one in the sandbox.
	PublishPort(ctx context.Context, name string, p Port) error
	// UnpublishPort stops forwarding a port List reported.
	UnpublishPort(ctx context.Context, name string, p Port) error
	// CopyIn copies a file on the host to dst in a running sandbox. dst's
	// directory must exist.
	CopyIn(ctx context.Context, name, src, dst string) error
	// Exec runs a bash script in the sandbox's workspace and returns stdout.
	Exec(ctx context.Context, name, script string) (string, error)
	// Interactive returns a command that takes over the terminal.
	Interactive(name string, argv ...string) *exec.Cmd
}
