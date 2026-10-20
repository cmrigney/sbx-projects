package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type CLI struct {
	Bin string
}

func NewCLI(bin string) *CLI {
	if bin == "" {
		bin = "sbx"
	}
	return &CLI{Bin: bin}
}

func (c *CLI) run(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.String(), fmt.Errorf("sbx %s: %s", args[0], lastLine(msg))
		}
		return out.String(), fmt.Errorf("sbx %s: %w", args[0], err)
	}
	return out.String(), nil
}

func (c *CLI) List(ctx context.Context) ([]Sandbox, error) {
	out, err := c.run(ctx, "", "ls", "--json")
	if err != nil {
		return nil, err
	}
	return parseList([]byte(out))
}

func parseList(b []byte) ([]Sandbox, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil, nil
	}
	var list []Sandbox
	if b[0] == '[' {
		err := json.Unmarshal(b, &list)
		return list, err
	}
	var wrapped struct {
		Sandboxes []Sandbox `json:"sandboxes"`
	}
	if err := json.Unmarshal(b, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Sandboxes, nil
}

func (c *CLI) MCPServers(ctx context.Context) ([]MCPServer, error) {
	out, err := c.run(ctx, "", "mcp", "ls", "--json")
	if err != nil {
		return nil, err
	}
	b := bytes.TrimSpace([]byte(out))
	if len(b) == 0 || string(b) == "null" {
		return nil, nil
	}
	var list []MCPServer
	err = json.Unmarshal(b, &list)
	return list, err
}

func (c *CLI) Create(ctx context.Context, o CreateOptions) error {
	args := []string{"run", "-d", "--name", o.Name}
	if o.Clone {
		args = append(args, "--clone")
	}
	for _, k := range o.Kits {
		args = append(args, "--kit", k)
	}
	if len(o.MCPs) > 0 {
		args = append(args, "--static-mcp", strings.Join(o.MCPs, ","))
	}
	args = append(args, o.Agent, o.Path)
	_, err := c.run(ctx, "", args...)
	return err
}

func (c *CLI) Start(ctx context.Context, name string) error {
	_, err := c.run(ctx, "", "run", "-d", "--name", name)
	return err
}

func (c *CLI) Stop(ctx context.Context, name string) error {
	_, err := c.run(ctx, "", "stop", name)
	return err
}

func (c *CLI) Remove(ctx context.Context, name string) error {
	_, err := c.run(ctx, "", "rm", "-f", name)
	return err
}

// Exec feeds the script on stdin so it never has to survive argv quoting.
func (c *CLI) Exec(ctx context.Context, name, script string) (string, error) {
	return c.run(ctx, script, "exec", "-i", name, "bash", "-s")
}

func (c *CLI) Interactive(name string, argv ...string) *exec.Cmd {
	return exec.Command(c.Bin, append([]string{"exec", "-it", name}, argv...)...)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return s
}

// ErrNotFound reports that the sbx binary is not on PATH.
var ErrNotFound = errors.New("sbx not found on PATH")

func (c *CLI) Check() error {
	if _, err := exec.LookPath(c.Bin); err != nil {
		return ErrNotFound
	}
	return nil
}
