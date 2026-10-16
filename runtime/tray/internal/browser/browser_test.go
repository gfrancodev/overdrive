package browser

import (
	"strings"
	"testing"
)

func TestCommandUsesUIURL(t *testing.T) {
	name, args, err := CommandFor("linux", "http://127.0.0.1:7742/")
	if err != nil {
		t.Fatal(err)
	}
	if name != "xdg-open" || len(args) != 1 || args[0] != "http://127.0.0.1:7742/" {
		t.Fatalf("%s %v", name, args)
	}
	name, args, err = CommandFor("darwin", "http://127.0.0.1:7742/settings")
	if err != nil || name != "open" || args[0] != "http://127.0.0.1:7742/settings" {
		t.Fatalf("darwin %s %v %v", name, args, err)
	}
	name, args, err = CommandFor("windows", "http://localhost:7742/")
	if err != nil || name != "rundll32" || strings.Join(args, " ") != "url.dll,FileProtocolHandler http://localhost:7742/" {
		t.Fatalf("windows %s %v %v", name, args, err)
	}
	if _, _, err := CommandFor("linux", "http://example.com/"); err == nil {
		t.Fatal("opened a public url")
	}
}
