package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

const cwdFlagHelp = "working directory"

type commandFn func([]string) error

var runtimeCommands = map[string]commandFn{
	"status":        cmdStatus,
	"project":       cmdProject,
	"session-start": cmdSessionStart,
	"session-end":   cmdSessionEnd,
	"record":        cmdRecord,
	"recall":        cmdRecall,
	"validate":      cmdValidate,
	"ledger-add":    cmdLedgerAdd,
	"ledger-list":   cmdLedgerList,
	"handoff-claim": cmdHandoffClaim,
	"before-prompt": cmdBeforePrompt,
	"context-file":  cmdContextFile,
	"index-tail":    cmdIndexTail,
	"mcp":           cmdMCP,
	"share":         cmdShare,
	"session-host":  cmdSessionHost,
	"version":       cmdVersion,
	"--version":     cmdVersion,
	"-v":            cmdVersion,
}

func cmdVersion([]string) error {
	fmt.Println(version)
	return nil
}

func runCLI(args []string) int {
	if len(args) < 1 {
		fail("usage: overdrive-runtime <status|project|session-start|session-end|record|recall|validate|ledger-add|ledger-list|share|session-host|version>")
		return 2
	}
	handler, ok := runtimeCommands[args[0]]
	if !ok {
		fail("unknown command: " + args[0])
		return 2
	}
	if err := handler(args[1:]); err != nil {
		fail(err.Error())
		return 2
	}
	return 0
}

func main() {
	exitProcess(runCLI(os.Args[1:]))
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		count, err := e.memoryCount()
		if err != nil {
			return err
		}
		return writeJSON(StatusResponse{
			Version:     version,
			Home:        e.home,
			Store:       dbPath(e.home),
			VectorIndex: vectorIndexPath(e.home),
			MemoryCount: count,
			Backend:     backendName,
			TurboVec:    e.tv != nil && e.tv.Available(),
			Embedder:    embedderName(),
			EmbedderDim: embedDim(),
		})
	})
}

func cmdProject(args []string) error {
	fs := flag.NewFlagSet("project", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return writeJSON(project)
}

func cmdSessionStart(args []string) error {
	fs := flag.NewFlagSet("session-start", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	quiet := fs.Bool("quiet", false, "suppress output")
	format := fs.String("format", "json", "json|text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		sessionID, err := e.startSession(project)
		if err != nil {
			return err
		}
		share, _ := e.shareStatus(project)
		if *quiet {
			return nil
		}
		pin := e.renderPin(project)
		if strings.EqualFold(*format, "text") {
			fmt.Print(pin)
			return nil
		}
		return writeJSON(map[string]any{"version": version, "project": project, "session_id": sessionID, "share": share, "pin": pin})
	})
}

func cmdSessionEnd(args []string) error {
	fs := flag.NewFlagSet("session-end", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	quiet := fs.Bool("quiet", false, "suppress output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		if err := e.endSession(project); err != nil {
			return err
		}
		if *quiet {
			return nil
		}
		share, _ := e.shareStatus(project)
		return writeJSON(map[string]any{"version": version, "project": project, "share": share})
	})
}

func cmdRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	kind := fs.String("kind", "episode", "memory kind")
	scope := fs.String("scope", "repository", "global|organization|repository|module")
	subject := fs.String("subject", "", "memory subject")
	content := fs.String("content", "", "memory content")
	confidence := fs.Float64("confidence", 0.7, "confidence from 0 to 1")
	priority := fs.Int("priority", 50, "priority from 0 to 100")
	source := fs.String("source", "agent_observation", "memory source")
	sourceRef := fs.String("source-ref", "", "source reference")
	evidence := fs.String("evidence", "", "supporting evidence")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*content) == "" {
		return fmt.Errorf("--content is required")
	}
	if !validKind(*kind) {
		return fmt.Errorf("invalid --kind %q", *kind)
	}
	if !validScope(*scope) {
		return fmt.Errorf("invalid --scope %q", *scope)
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		m, err := e.recordMemory(memoryRecord{Project: project, Kind: *kind, Scope: *scope, Subject: *subject, Content: *content, Confidence: *confidence, Priority: *priority, Source: *source, SourceRef: *sourceRef, Evidence: *evidence})
		if err != nil {
			return err
		}
		return writeJSON(m)
	})
}

func cmdRecall(args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	query := fs.String("query", "", "retrieval query")
	limit := fs.Int("limit", 12, "max regular memories")
	layer := fs.String("layer", "", "knowledge|lessons|episodes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		resp, err := e.recall(project, *query, *limit, *layer)
		if err != nil {
			return err
		}
		return writeJSON(resp)
	})
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	id := fs.String("id", "", "memory id")
	result := fs.String("result", "", "success|failure|contradiction")
	winner := fs.String("winner-note", "", "conflict resolution note")
	replaces := fs.String("replaces-id", "", "successor memory id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *result == "" {
		return fmt.Errorf("--id and --result are required")
	}
	return withEngine(func(e *Engine) error {
		m, err := e.validateMemory(*id, *result, *winner, *replaces)
		if err != nil {
			return err
		}
		return writeJSON(m)
	})
}

func cmdLedgerAdd(args []string) error {
	fs := flag.NewFlagSet("ledger-add", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	runID := fs.String("run", "", "run identifier")
	decision := fs.String("decision", "", "decision ruling")
	evidence := fs.String("evidence", "", "supporting evidence")
	reason := fs.String("reason", "", "reason")
	risk := fs.String("risk", "", "risk if wrong")
	reversibility := fs.String("reversibility", "", "reversibility")
	lifetime := fs.String("lifetime", "run", "run|standing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" || *decision == "" {
		return fmt.Errorf("--run and --decision are required")
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		entry, err := e.ledgerAddLifetime(ledgerWrite{
			Project: project, RunID: *runID, Decision: *decision, Evidence: *evidence,
			Reason: *reason, Risk: *risk, Reversibility: *reversibility, Lifetime: *lifetime,
		})
		if err != nil {
			return err
		}
		return writeJSON(entry)
	})
}

func cmdLedgerList(args []string) error {
	fs := flag.NewFlagSet("ledger-list", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	runID := fs.String("run", "", "run identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		return fmt.Errorf("--run is required")
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		entries, err := e.ledgerList(project, *runID)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"run_id": *runID, "entries": entries})
	})
}

func cmdHandoffClaim(args []string) error {
	fs := flag.NewFlagSet("handoff-claim", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		claimed, err := e.claimOpenHandoff(project, localDeviceID(e.home))
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"claimed": claimed})
	})
}

func cmdBeforePrompt(args []string) error {
	fs := flag.NewFlagSet("before-prompt", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	payload := fs.String("payload", "", "hook payload")
	payloadFile := fs.String("payload-file", "", "hook payload file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body := *payload
	if strings.TrimSpace(*payloadFile) != "" {
		raw, err := os.ReadFile(*payloadFile)
		if err != nil {
			return err
		}
		body = string(raw)
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	query := queryFromHookPayload(body)
	return withEngine(func(e *Engine) error {
		_, _ = e.claimOpenHandoff(project, localDeviceID(e.home))
		fmt.Print(e.renderPin(project))
		if query == "" {
			return nil
		}
		resp, err := e.recall(project, query, 4, "")
		if err != nil {
			return err
		}
		for _, hint := range resp.Unconfirmed {
			fmt.Println("pista não confirmada: " + hint)
		}
		return nil
	})
}

func cmdContextFile(args []string) error {
	fs := flag.NewFlagSet("context-file", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	out := fs.String("out", "", "context file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return fmt.Errorf("--out is required")
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		return os.WriteFile(*out, []byte(e.renderPin(project)), 0o600)
	})
}

func cmdIndexTail(args []string) error {
	fs := flag.NewFlagSet("index-tail", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	file := fs.String("file", "", "jsonl path")
	harness := fs.String("harness", "harness", "harness id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*file) == "" {
		return fmt.Errorf("--file is required")
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		moved, err := e.indexTranscriptFile(project, *harness, *file)
		if err != nil {
			return err
		}
		if moved {
			_ = e.ensureHandoffFromHints(project)
		}
		return writeJSON(map[string]any{"moved": moved})
	})
}

func queryFromHookPayload(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}
	for _, key := range []string{"prompt", "user_prompt", "content", "text", "query"} {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
