package rancher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/httpstream"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecOptions describes a command to run inside a container.
type ExecOptions struct {
	// Command is the argv to execute, e.g. []string{"sh", "-c", "id"}.
	Command []string
	// Container selects a container in the pod. Empty means the only/first one.
	Container string
	// Stdin is written to the command's standard input before it is closed.
	Stdin string
	// Timeout bounds the whole execution. Zero means no explicit deadline.
	Timeout time.Duration
}

// ExecResult carries the outcome of a remote command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command in a container of a pod via the Kubernetes exec
// subresource, proxied through Rancher at /k8s/clusters/<clusterID>/...
//
// Transport: the WebSocket executor is tried first and SPDY is used as a
// fallback, because exec-over-websocket only became an API server feature in
// Kubernetes 1.29, while older clusters (e.g. 1.16) only speak SPDY.
//
// A non-zero exit code is reported in ExecResult.ExitCode (not as an error);
// transport, authorization and other failures are returned as errors.
func (c *SteveClient) Exec(ctx context.Context, clusterID, namespace, podName string, opts ExecOptions) (*ExecResult, error) {
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("exec: command is required")
	}

	q := url.Values{}
	for _, a := range opts.Command {
		q.Add("command", a)
	}
	if opts.Container != "" {
		q.Set("container", opts.Container)
	}
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	q.Set("tty", "false")
	if opts.Stdin != "" {
		q.Set("stdin", "true")
	} else {
		q.Set("stdin", "false")
	}

	execURL := fmt.Sprintf("%s/k8s/clusters/%s/api/v1/namespaces/%s/pods/%s/exec?%s",
		strings.TrimRight(c.baseURL, "/"), clusterID, namespace, podName, q.Encode())
	u, err := url.Parse(execURL)
	if err != nil {
		return nil, fmt.Errorf("exec: parse url: %w", err)
	}

	cfg := &restclient.Config{
		Host:            c.baseURL,
		BearerToken:     c.token,
		TLSClientConfig: restclient.TLSClientConfig{Insecure: c.insecure},
		UserAgent:       "rancher-mcp-server",
	}

	wsExec, wsErr := remotecommand.NewWebSocketExecutor(cfg, "POST", execURL)
	spdyExec, spdyErr := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if wsErr != nil && spdyErr != nil {
		return nil, fmt.Errorf("exec: cannot build executor (websocket: %v; spdy: %v)", wsErr, spdyErr)
	}

	var executor remotecommand.Executor
	switch {
	case wsErr == nil && spdyErr == nil:
		executor, err = remotecommand.NewFallbackExecutor(wsExec, spdyExec, shouldFallbackToSPDY)
		if err != nil {
			return nil, fmt.Errorf("exec: build fallback executor: %w", err)
		}
	case wsErr == nil:
		executor = wsExec
	default:
		executor = spdyExec
	}

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	var stdout, stderr bytes.Buffer
	streamOpts := remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr, Tty: false}
	if opts.Stdin != "" {
		streamOpts.Stdin = bytes.NewReader([]byte(opts.Stdin))
	}

	runErr := executor.StreamWithContext(runCtx, streamOpts)
	res := &ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if runErr != nil {
		if code, ok := exitCodeFromError(runErr); ok {
			res.ExitCode = code
			return res, nil
		}
		return res, fmt.Errorf("exec: %w", runErr)
	}
	return res, nil
}

// shouldFallbackToSPDY reports whether a failed websocket attempt should be
// retried over SPDY. Only transport/upgrade failures qualify: an auth or
// permission error will fail the same way over SPDY, so retrying is pointless.
func shouldFallbackToSPDY(err error) bool {
	if err == nil {
		return false
	}
	if httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "forbidden") || strings.Contains(msg, "unauthorized") {
		return false
	}
	return true
}

var exitCodePattern = regexp.MustCompile(`exit code (\d+)`)

// exitCodeFromError extracts the remote exit code of a terminated command.
// The API server reports it as a Status with an "ExitCode" cause, but the
// remotecommand transports collapse that into a plain error whose message
// reads "command terminated with exit code N", so both shapes are handled.
func exitCodeFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		st := status.Status()
		if st.Details != nil {
			for _, cause := range st.Details.Causes {
				if string(cause.Type) == "ExitCode" {
					if n, cerr := strconv.Atoi(cause.Message); cerr == nil {
						return n, true
					}
				}
			}
		}
	}
	if m := exitCodePattern.FindStringSubmatch(err.Error()); m != nil {
		if n, cerr := strconv.Atoi(m[1]); cerr == nil {
			return n, true
		}
	}
	return 0, false
}
