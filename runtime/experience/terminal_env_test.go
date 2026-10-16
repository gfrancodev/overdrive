package main

import "testing"

func TestEnsurePTYEnvOverridesDumbTERM(t *testing.T) {
	got := ensurePTYEnv([]string{"PATH=/bin", "TERM=dumb", "COLORTERM="})
	if envGet(got, "TERM") != "xterm-256color" {
		t.Fatalf("TERM=%q", envGet(got, "TERM"))
	}
	if envGet(got, "COLORTERM") != "truecolor" {
		t.Fatalf("COLORTERM=%q", envGet(got, "COLORTERM"))
	}
	if envGet(got, "TERM_PROGRAM") != "Overdrive" {
		t.Fatalf("TERM_PROGRAM=%q", envGet(got, "TERM_PROGRAM"))
	}
}

func TestEnsurePTYEnvKeeps256ColorTERM(t *testing.T) {
	got := ensurePTYEnv([]string{"TERM=tmux-256color", "COLORTERM=truecolor"})
	if envGet(got, "TERM") != "tmux-256color" {
		t.Fatalf("TERM=%q", envGet(got, "TERM"))
	}
}

func TestEnsurePTYEnvDropsColorKillersAndHostAgentVars(t *testing.T) {
	got := ensurePTYEnv([]string{
		"PATH=/bin",
		"NO_COLOR=1",
		"FORCE_COLOR=0",
		"CLICOLOR_FORCE=1",
		"CURSOR_AGENT=1",
		"__CURSOR_SANDBOX_ENV_RESTORE=x",
		"VSCODE_PID=1",
		"CLAUDECODE=1",
		"TERM_PROGRAM=vscode",
		"LS_COLORS=di=01;34",
	})
	for _, key := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR_FORCE", "CURSOR_AGENT", "__CURSOR_SANDBOX_ENV_RESTORE", "VSCODE_PID", "CLAUDECODE"} {
		if envHasKey(got, key) {
			t.Fatalf("%s leaked into PTY env: %v", key, got)
		}
	}
	if envGet(got, "TERM_PROGRAM") != "Overdrive" {
		t.Fatalf("TERM_PROGRAM=%q", envGet(got, "TERM_PROGRAM"))
	}
	if envGet(got, "LS_COLORS") != "di=01;34" || envGet(got, "PATH") != "/bin" {
		t.Fatalf("user vars lost: %v", got)
	}
}

func TestEnsurePTYEnvTrimsBashPromptDir(t *testing.T) {
	got := ensurePTYEnv([]string{"TERM=xterm-256color", "COLORTERM=truecolor"})
	if envGet(got, "PROMPT_DIRTRIM") != "1" {
		t.Fatalf("PROMPT_DIRTRIM=%q", envGet(got, "PROMPT_DIRTRIM"))
	}
	kept := ensurePTYEnv([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "PROMPT_DIRTRIM=3"})
	if envGet(kept, "PROMPT_DIRTRIM") != "3" {
		t.Fatalf("user PROMPT_DIRTRIM overwritten: %q", envGet(kept, "PROMPT_DIRTRIM"))
	}
}

func TestFilterTerminalEnvBlocksSecretSuffixes(t *testing.T) {
	out := filterTerminalEnv([]string{"GEMINI_API_KEY=x", "STRIPE_SECRET=y", "NPM_TOKEN=z", "DB_PASSWORD=w", "TOKENIZERS_PARALLELISM=false"})
	for _, key := range []string{"GEMINI_API_KEY", "STRIPE_SECRET", "NPM_TOKEN", "DB_PASSWORD"} {
		if envHasKey(out, key) {
			t.Fatalf("%s leaked: %v", key, out)
		}
	}
	if !envHasKey(out, "TOKENIZERS_PARALLELISM") {
		t.Fatalf("non-secret var dropped: %v", out)
	}
}
