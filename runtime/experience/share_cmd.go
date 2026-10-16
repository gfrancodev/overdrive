package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const circleIDFlagHelp = "circle id"

var shareCommands = map[string]func([]string) error{
	"identity": cmdShareIdentity,
	"circle":   cmdShareCircle,
	"listen":   cmdShareListen,
	"status":   cmdShareStatus,
}

func cmdShare(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: share <identity|circle> ...")
	}
	handler, ok := shareCommands[args[0]]
	if !ok {
		return fmt.Errorf("unknown share command: %s", args[0])
	}
	return handler(args[1:])
}

func cmdShareListen(args []string) error {
	fs := flag.NewFlagSet("share listen", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN")) == "" {
		return fmt.Errorf("OVERDRIVE_SHARE_LISTEN is required for share listen")
	}
	home, err := ensureHome()
	if err != nil {
		return err
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	loadMeshState(home)
	maybePruneIdleCirclesOnListen(home)
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		return err
	}
	startShareDashboard(home)
	startMeshServices(home)
	_ = writeShareListenProjectRoot(home, *cwd)
	shareListenWait(sl)
	return nil
}

var shareListenWait = func(sl *shareListener) {
	if hookShareListenWait != nil {
		hookShareListenWait(sl)
		return
	}
	if sl == nil || sl.stop == nil {
		return
	}
	<-sl.stop
}

func cmdShareStatus(args []string) error {
	fs := flag.NewFlagSet("share status", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", cwdFlagHelp)
	format := fs.String("format", "json", "json|text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	project, err := identifyProject(*cwd)
	if err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		status, err := e.shareStatus(project)
		if err != nil {
			return err
		}
		circle, ok := circleForProject(e.home, project.Root)
		groupKey := ""
		if ok {
			groupKey = circle.GroupKey
		}
		if err := rejectStatusGroupKey(status, groupKey); err != nil {
			return err
		}
		if strings.EqualFold(*format, "text") {
			fmt.Println(formatShareStatusText(status))
			return nil
		}
		return writeJSON(status)
	})
}

func cmdShareIdentity(args []string) error {
	fs := flag.NewFlagSet("share identity", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		id, _, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{
			"device_id":  id.DeviceID,
			"public_key": id.PublicKey,
			"created_at": id.CreatedAt,
		})
	})
}

var circleCommands = map[string]func([]string) error{
	"create":      cmdCircleCreate,
	"invite":      cmdCircleInvite,
	"accept":      cmdCircleAccept,
	"revoke":      cmdCircleRevoke,
	"list":        cmdCircleList,
	"folder-add":  cmdCircleFolderAdd,
	"folder-list": cmdCircleFolderList,
	"prune":       cmdCirclePrune,
}

func cmdShareCircle(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: share circle <create|invite|accept|revoke|list|folder-add|folder-list|prune>")
	}
	handler, ok := circleCommands[args[0]]
	if !ok {
		return fmt.Errorf("unknown circle command: %s", args[0])
	}
	return handler(args[1:])
}

func cmdCircleCreate(args []string) error {
	fs := flag.NewFlagSet("share circle create", flag.ContinueOnError)
	name := fs.String("name", "", "circle name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	return withEngine(func(e *Engine) error {
		id, priv, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		c, err := createCircle(e.home, *name, id, priv)
		if err != nil {
			return err
		}
		return writeJSON(c)
	})
}

func cmdCircleInvite(args []string) error {
	fs := flag.NewFlagSet("share circle invite", flag.ContinueOnError)
	circleID := fs.String("circle", "", circleIDFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *circleID == "" {
		return fmt.Errorf("--circle is required")
	}
	return withEngine(func(e *Engine) error {
		id, _, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		invite, err := createInvite(e.home, *circleID, id)
		if err != nil {
			return err
		}
		return writeJSON(invite)
	})
}

func cmdCircleAccept(args []string) error {
	fs := flag.NewFlagSet("share circle accept", flag.ContinueOnError)
	code := fs.String("code", "", "invite code")
	fingerprint := fs.String("fingerprint", "", "invite fingerprint")
	endpoint := fs.String("peer", "", "peer endpoint host:port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *code == "" || *fingerprint == "" {
		return fmt.Errorf("--code and --fingerprint are required")
	}
	return withEngine(func(e *Engine) error {
		id, priv, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		c, err := acceptInvite(e.home, *code, *fingerprint, *endpoint, id, priv)
		if err != nil {
			return err
		}
		return writeJSON(c)
	})
}

func cmdCircleRevoke(args []string) error {
	fs := flag.NewFlagSet("share circle revoke", flag.ContinueOnError)
	circleID := fs.String("circle", "", circleIDFlagHelp)
	deviceID := fs.String("device", "", "device id to revoke")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *circleID == "" || *deviceID == "" {
		return fmt.Errorf("--circle and --device are required")
	}
	return withEngine(func(e *Engine) error {
		id, priv, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		c, err := revokeMember(e.home, *circleID, *deviceID, id, priv)
		if err != nil {
			return err
		}
		_ = e.markPeerRevoked(*circleID, *deviceID)
		return writeJSON(c)
	})
}

func cmdCircleList(args []string) error {
	fs := flag.NewFlagSet("share circle list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		circles, err := listCircles(e.home)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"circles": circles})
	})
}

func cmdCircleFolderAdd(args []string) error {
	fs := flag.NewFlagSet("share circle folder-add", flag.ContinueOnError)
	circleID := fs.String("circle", "", circleIDFlagHelp)
	folder := fs.String("folder", "", "allowed folder path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *circleID == "" || *folder == "" {
		return fmt.Errorf("--circle and --folder are required")
	}
	return withEngine(func(e *Engine) error {
		id, _, err := loadOrCreateIdentity(e.home)
		if err != nil {
			return err
		}
		c, err := addAllowedFolder(e.home, *circleID, *folder, id)
		if err != nil {
			return err
		}
		return writeJSON(c)
	})
}

func cmdCircleFolderList(args []string) error {
	fs := flag.NewFlagSet("share circle folder-list", flag.ContinueOnError)
	circleID := fs.String("circle", "", circleIDFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *circleID == "" {
		return fmt.Errorf("--circle is required")
	}
	return withEngine(func(e *Engine) error {
		c, err := loadCircle(e.home, *circleID)
		if err != nil {
			return err
		}
		return writeJSON(map[string]any{"circle_id": c.ID, "allowed_folders": c.AllowedFolders})
	})
}

func cmdCirclePrune(args []string) error {
	fs := flag.NewFlagSet("share circle prune", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "list circles that would be removed without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withEngine(func(e *Engine) error {
		report, err := pruneIdleCircles(e.home, *dryRun)
		if err != nil {
			return err
		}
		return writeJSON(report)
	})
}
