package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: overdrive-runtime <status|project|session-start|record|recall|validate|ledger-add|ledger-list|version>")
	}

	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus(os.Args[2:])
	case "project":
		err = cmdProject(os.Args[2:])
	case "session-start":
		err = cmdSessionStart(os.Args[2:])
	case "record":
		err = cmdRecord(os.Args[2:])
	case "recall":
		err = cmdRecall(os.Args[2:])
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "ledger-add":
		err = cmdLedgerAdd(os.Args[2:])
	case "ledger-list":
		err = cmdLedgerList(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	default:
		fail("unknown command: " + os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
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
		if *quiet {
			return nil
		}
		return writeJSON(map[string]any{"version": version, "project": project, "session_id": sessionID})
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
