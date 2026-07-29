package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Carrier drives the high-level session interactions — input delivery, output
// capture, interrupt, scrollback — over a connection to the box, separating
// HOW an interaction is realized from the connection that reaches the box. Its
// scope is exactly those four driving verbs; liveness, attachment, activity,
// and metadata stay provider-specific, and higher-level orchestration
// (startup-dialog acceptance, no-wait nudge, wait-for-idle) is composed ABOVE a
// Carrier out of Peek/SendKeys, not added to it.
//
// Every op returns the underlying transport error verbatim. Whether a failure
// is fatal or best-effort is the PROVIDER facade's policy: a provider decides
// per verb which errors to discard when it delegates here (e.g. Kubernetes
// treats a missing pod as a no-op for SendKeys but propagates a genuine
// transport failure to a live pod, and propagates both for Nudge) — the
// Carrier itself never swallows.
//
// The tmux carrier ([NewTmuxCarrier]) realizes these verbs by issuing tmux
// commands over an [ExecProvider]. It is the shared driver for tmux-in-a-box
// exec-connection runtimes (Kubernetes, the SSH backend, and packs that opt
// into the tmux-box model). Such a provider must expose a name-keyed
// [ExecProvider] and owns name->box and name->target resolution plus any
// best-effort swallowing in its own adapter — the carrier knows nothing about
// pods or how the box is reached. Adopting it for a runtime that drives input
// via its own dedicated ops (e.g. an exec pack's nudge/peek subcommands) is a
// protocol change, not a silent migration. The local tmux control driver and
// the ACP stream driver realize these verbs differently and keep their own
// behavior.
type Carrier interface {
	// Nudge delivers content as input to the session, followed by a submit.
	Nudge(ctx context.Context, name string, content []ContentBlock) error
	// SendKeys sends bare keystrokes (e.g. "Enter", "C-c") without a submit.
	SendKeys(ctx context.Context, name string, keys ...string) error
	// Peek captures the last lines of output (all scrollback when lines <= 0).
	Peek(ctx context.Context, name string, lines int) (string, error)
	// Interrupt sends a soft interrupt (Ctrl-C) to the session.
	Interrupt(ctx context.Context, name string) error
	// ClearScrollback clears the session's scrollback history.
	ClearScrollback(ctx context.Context, name string) error
}

// tmuxCarrier drives a tmux session living inside the box by issuing tmux
// commands over an [ExecProvider] connection. target is the in-box tmux session
// the commands address (e.g. "main"); it is fixed per carrier today — a
// name->target resolver is the natural extension if one connection ever
// multiplexes sessions on distinct targets. The mapping mirrors the tmux
// commands the Kubernetes provider issues over execInPod today, so once k8s
// exposes an [ExecProvider], delegating its driving methods here is
// argv-for-argv behavior-preserving (the provider keeps its own per-verb
// error policy; see [Carrier]).
type tmuxCarrier struct {
	conn   ExecProvider
	target string
}

var tmuxCarrierBufferSeq uint64

const tmuxCarrierLiteralLimit = 4096

// Match the mature local tmux delivery path: provider TUIs need a short
// boundary between accepting a paste/key burst and receiving the submit Enter.
// Without it, tmux can report success while a detached Codex pane leaves the
// text drafted and drops the Enter.
const tmuxCarrierSubmitDebounce = 500 * time.Millisecond

// NewTmuxCarrier returns a [Carrier] that drives the in-box tmux session
// target over conn.
func NewTmuxCarrier(conn ExecProvider, target string) Carrier {
	return &tmuxCarrier{conn: conn, target: target}
}

// tmux runs `tmux <args...>` in the box over the connection and returns its
// standard output. ExecProvider distinguishes a command exit from a transport
// failure with its integer result, so preserve both as errors here; otherwise a
// missing tmux session looks like successful input delivery over providers such
// as Kubernetes.
func (c *tmuxCarrier) tmux(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, code, err := c.conn.Exec(ctx, name, append([]string{"tmux"}, args...))
	if err != nil {
		return out, err
	}
	if code != 0 {
		command := "command"
		if len(args) > 0 {
			command = args[0]
		}
		return out, fmt.Errorf("tmux %s exited with status %d", command, code)
	}
	return out, nil
}

func (c *tmuxCarrier) Nudge(ctx context.Context, name string, content []ContentBlock) error {
	message := FlattenText(content)
	if message == "" {
		return nil
	}

	// Keep the two-call fast path for ordinary one-line nudges. A rendered
	// startup prompt is multiline, however: send-keys -l turns its newlines into
	// a stream of synthetic terminal keys, so a provider TUI can submit fragments
	// or exit before the final Enter. Large single-line messages can also exceed
	// the remote command boundary. The local tmux driver already uses an atomic
	// bracketed paste for both shapes; tmux-in-a-box providers must match it.
	if !strings.ContainsAny(message, "\r\n") && len(message) <= tmuxCarrierLiteralLimit {
		if _, err := c.tmux(ctx, name, "send-keys", "-t", c.target, "-l", message); err != nil {
			return err
		}
		_, err := c.tmux(ctx, name, "send-keys", "-t", c.target, "Enter")
		return err
	}

	buffer := "gc-carrier-nudge-" + strconv.FormatUint(atomic.AddUint64(&tmuxCarrierBufferSeq, 1), 10)
	if _, err := c.tmux(ctx, name, "set-buffer", "-b", buffer, "--", message); err != nil {
		return err
	}
	loaded := true
	defer func() {
		if loaded {
			_, _ = c.tmux(context.Background(), name, "delete-buffer", "-b", buffer)
		}
	}()
	if _, err := c.tmux(ctx, name, "paste-buffer", "-p", "-d", "-b", buffer, "-t", c.target); err != nil {
		return err
	}
	loaded = false
	time.Sleep(tmuxCarrierSubmitDebounce)
	_, err := c.tmux(ctx, name, "send-keys", "-t", c.target, "Enter")
	return err
}

func (c *tmuxCarrier) SendKeys(ctx context.Context, name string, keys ...string) error {
	// k8s issues a bare no-op `send-keys -t <target>` for empty keys; we
	// short-circuit to issue nothing (behaviorally identical — no keystrokes).
	if len(keys) == 0 {
		return nil
	}
	_, err := c.tmux(ctx, name, append([]string{"send-keys", "-t", c.target}, keys...)...)
	return err
}

func (c *tmuxCarrier) Peek(ctx context.Context, name string, lines int) (string, error) {
	start := "-"
	if lines > 0 {
		start = "-" + strconv.Itoa(lines)
	}
	out, err := c.tmux(ctx, name, "capture-pane", "-t", c.target, "-p", "-S", start)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c *tmuxCarrier) Interrupt(ctx context.Context, name string) error {
	_, err := c.tmux(ctx, name, "send-keys", "-t", c.target, "C-c")
	return err
}

func (c *tmuxCarrier) ClearScrollback(ctx context.Context, name string) error {
	_, err := c.tmux(ctx, name, "clear-history", "-t", c.target)
	return err
}
