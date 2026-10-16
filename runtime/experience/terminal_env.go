package main

import "strings"

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

func envGet(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func envSet(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			if !found {
				out = append(out, prefix+value)
				found = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, prefix+value)
	}
	return out
}

func capableTERM(term string) bool {
	t := strings.ToLower(strings.TrimSpace(term))
	if t == "" {
		return false
	}
	switch t {
	case "dumb", "unknown", "cons25", "vt100", "vt102", "ansi", "linux", "xterm", "screen", "tmux":
		return false
	}
	return strings.Contains(t, "256") || strings.Contains(t, "direct") || strings.Contains(t, "truecolor")
}

// The runtime is often launched by an IDE or coding agent whose env disables
// colors or marks the process as non-interactive; an interactive PTY must not
// inherit that.
var ptyEnvExactDrop = map[string]bool{
	"NO_COLOR":             true,
	"FORCE_COLOR":          true,
	"CLICOLOR_FORCE":       true,
	"CLAUDECODE":           true,
	"AGENT_TRANSCRIPTS":    true,
	"TERM_PROGRAM_VERSION": true,
}

var ptyEnvPrefixDrop = []string{"CURSOR_", "__CURSOR_", "VSCODE_", "CLAUDE_CODE_"}

func dropPTYHostEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := strings.ToUpper(kv[:eq])
		if ptyEnvExactDrop[key] {
			continue
		}
		drop := false
		for _, p := range ptyEnvPrefixDrop {
			if strings.HasPrefix(key, p) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func ensurePTYEnv(env []string) []string {
	out := dropPTYHostEnv(env)
	if !capableTERM(envGet(out, "TERM")) {
		out = envSet(out, "TERM", "xterm-256color")
	}
	if strings.TrimSpace(envGet(out, "COLORTERM")) == "" {
		out = envSet(out, "COLORTERM", "truecolor")
	}
	out = envSet(out, "TERM_PROGRAM", "Overdrive")
	// Bash \w follows the physical cwd. Circle session dirs live under a long
	// workspaces/…/sessions/… path, so an untrimmed prompt wraps and fills the pane.
	// Keep the user's PS1; only the trailing directory is shown unless they set this.
	if strings.TrimSpace(envGet(out, "PROMPT_DIRTRIM")) == "" {
		out = envSet(out, "PROMPT_DIRTRIM", "1")
	}
	return out
}
