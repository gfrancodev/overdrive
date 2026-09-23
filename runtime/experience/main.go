package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

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
	"share":         cmdShare,
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
		fail("usage: overdrive-runtime <status|project|session-start|session-end|record|recall|validate|ledger-add|ledger-list|share|version>")
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
	cwd := fs.String("cwd", ".", "working directory")
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
	cwd := fs.String("cwd", ".", "working directory")
	quiet := fs.Bool("quiet", false, "suppress output")
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
		return writeJSON(map[string]any{"version": version, "project": project, "session_id": sessionID, "share": share})
	})
}

func cmdSessionEnd(args []string) error {
	fs := flag.NewFlagSet("session-end", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", "working directory")
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
	cwd := fs.String("cwd", ".", "working directory")
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
		m, err := e.recordMemory(project, *kind, *scope, *subject, *content, *confidence, *priority, *source, *sourceRef, *evidence)
		if err != nil {
			return err
		}
		return writeJSON(m)
	})
}

func cmdRecall(args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", "working directory")
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
	cwd := fs.String("cwd", ".", "working directory")
	runID := fs.String("run", "", "run identifier")
	decision := fs.String("decision", "", "decision ruling")
	evidence := fs.String("evidence", "", "supporting evidence")
	reason := fs.String("reason", "", "reason")
	risk := fs.String("risk", "", "risk if wrong")
	reversibility := fs.String("reversibility", "", "reversibility")
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
		entry, err := e.ledgerAdd(project, *runID, *decision, *evidence, *reason, *risk, *reversibility)
		if err != nil {
			return err
		}
		return writeJSON(entry)
	})
}

func cmdLedgerList(args []string) error {
	fs := flag.NewFlagSet("ledger-list", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", "working directory")
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
