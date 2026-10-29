package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// `volt inbox-hook` is the Claude Code hook (SessionStart and Stop, async with
// asyncRewake) that Connect AI installs. It waits for a message from the IDE's
// editor (a question about selected code) and exits 2 with it on stderr, which wakes the idle session —
// in the terminal and in Claude Desktop's Code tab, on every OS, without
// bringing any window to the front. Async hooks have no timeout, so it waits as
// long as it must; one waiter per session.

var safeSession = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func runInboxHook(args []string) int {
	var in struct {
		SessionID string `json:"session_id"`
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	_ = json.Unmarshal(data, &in)
	// The same id `volt mcp` sends as X-Volt-Session, so the IDE can address
	// questions to this session.
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" {
		in.SessionID = sid
	}
	session := safeSession.ReplaceAllString(in.SessionID, "")
	if session == "" {
		session = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	lock := filepath.Join(home, ".volt", "hooks", session+".pid")
	if !takeLock(lock) {
		return 0 // this session already has a waiter
	}
	defer os.Remove(lock)

	clientName.Store("Claude Code")
	for {
		msgs, err := waitInbox(in.SessionID)
		if err != nil {
			time.Sleep(5 * time.Second) // IDE closed, no project open, or another agent attached
			continue
		}
		if len(msgs) > 0 {
			fmt.Fprint(os.Stderr, frameMessages(msgs))
			return 2
		}
	}
}

// takeLock creates lock with this process's pid, atomically, replacing a
// lock whose waiter has died.
func takeLock(lock string) bool {
	_ = os.MkdirAll(filepath.Dir(lock), 0o700)
	for range 2 {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprint(f, os.Getpid())
			f.Close()
			return true
		}
		b, _ := os.ReadFile(lock)
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid == 0 || processAlive(pid) {
			return false // alive, or another waiter is still writing its pid
		}
		os.Remove(lock)
	}
	return false
}

type inboxMessage struct {
	ID          int    `json:"id"`
	From        string `json:"from"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	Attachments []struct {
		Type      string `json:"type"`
		File      string `json:"file"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		Text      string `json:"text"`
	} `json:"attachments"`
}

// hookClient names the app this Claude Code session runs in: a terminal
// session ("cli") is Claude Code; anything else is Claude Desktop's Code tab.
// The IDE only hands a session the questions addressed to its app.
func hookClient() string {
	if os.Getenv("CLAUDE_CODE_ENTRYPOINT") == "cli" {
		return "Claude Code"
	}
	return "Claude Desktop"
}

// waitInbox attaches this session under its app and long-polls for unread messages.
func waitInbox(session string) ([]inboxMessage, error) {
	ctx := context.Background()
	client := hookClient()
	if _, err := ideCall(ctx, "agent.attach", map[string]any{"client": client, "session": session}, 10*time.Second); err != nil {
		return nil, err
	}
	raw, err := ideCall(ctx, "msg.wait", map[string]any{"fresh": true, "timeout_s": 50, "client": client, "session": session}, 65*time.Second)
	if err != nil {
		return nil, err
	}
	var out struct {
		Messages []inboxMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("bad inbox answer")
	}
	return out.Messages, nil
}

// frameMessages marks each message as coming from the Volt IDE, so Claude
// treats it as the user's request rather than untrusted hook output.
func frameMessages(msgs []inboxMessage) string {
	name := "user"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = filepath.Base(u.Username) // Windows: DOMAIN\name
	}
	var b strings.Builder
	for _, m := range msgs {
		who := name
		if m.From == "ide" {
			who = "IDE event"
		}
		fmt.Fprintf(&b, "[Volt IDE · %s · msg %d] %s\n", who, m.ID, m.Text)
		for _, a := range m.Attachments {
			if a.Type == "selection" {
				fmt.Fprintf(&b, "Selected in %s, lines %d–%d:\n```\n%s\n```\n", filepath.Base(a.File), a.StartLine, a.EndLine, a.Text)
			}
		}
	}
	b.WriteString("(Asked by the user from the Volt IDE editor about the selected code. Do what it asks, answer here, and make any change in the IDE with the volt ide_ tools.)\n")
	return b.String()
}
