package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mrostamii/rancher-mcp-server/pkg/client/rancher"
)

func (t *Toolset) execTool() mcp.Tool {
	return mcp.NewTool(
		"kubernetes_exec",
		mcp.WithDescription("Run a command inside a container of a pod (kubectl exec equivalent). Returns stdout, stderr and the exit code. Cluster is Rancher cluster ID. Requires the server to be started with exec enabled."),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster ID")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace of the pod")),
		mcp.WithString("pod", mcp.Required(), mcp.Description("Pod name")),
		mcp.WithString("container", mcp.Description("Container name (optional; default is first/only container)")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Command to run, e.g. \"id\" or \"sh -c 'ls -la /app'\". Passed as argv WITHOUT shell interpretation — shell syntax (pipes, ;, redirects) needs \"sh -c '...'\"")),
		mcp.WithString("stdin", mcp.Description("Optional data written to the command's standard input")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Timeout in seconds for the command (default: 60, max: 600)")),
	)
}

func (t *Toolset) execHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cluster, err := req.RequireString("cluster")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pod, err := req.RequireString("pod")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	commandLine, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := t.policy.CheckNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !t.policy.CanExec() {
		return mcp.NewToolResultError("exec operations are disabled (start the server with --enable-exec)"), nil
	}

	argv := splitCommand(commandLine)
	if len(argv) == 0 {
		return mcp.NewToolResultError("command is empty"), nil
	}

	timeout := time.Duration(req.GetInt("timeout_seconds", 60)) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 600*time.Second {
		timeout = 600 * time.Second
	}

	res, err := t.client.Exec(ctx, cluster, namespace, pod, rancher.ExecOptions{
		Command:   argv,
		Container: req.GetString("container", ""),
		Stdin:     req.GetString("stdin", ""),
		Timeout:   timeout,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("exec: %v", err)), nil
	}

	var b strings.Builder
	if res.Stdout != "" {
		b.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(res.Stderr)
	}
	b.WriteString(fmt.Sprintf("\n--- exit code: %d ---", res.ExitCode))
	return mcp.NewToolResultText(b.String()), nil
}

// splitCommand splits a command line into argv, honouring single and double
// quotes and backslash escapes so that arguments containing spaces survive.
func splitCommand(s string) []string {
	var (
		args    []string
		cur     strings.Builder
		inWord  bool
		quote   byte
		escaped bool
	)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			cur.WriteByte(ch)
			escaped = false
		case ch == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteByte(ch)
			}
		case ch == '\'' || ch == '"':
			quote = ch
			inWord = true
		case ch == ' ' || ch == '\t' || ch == '\n':
			if inWord || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if escaped {
		cur.WriteByte('\\')
	}
	if inWord || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}
