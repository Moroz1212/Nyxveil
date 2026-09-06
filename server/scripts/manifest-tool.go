//go:build ignore

// manifest-tool provides jq/python-free helpers for live-final-update tests and
// constrained hosts. Production Ubuntu nodes normally use jq.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nyxveil/server/internal/updater"
)

func main() {
	if len(os.Args) < 3 {
		fatal("usage: manifest-tool canon|field FILE [EXPR]")
	}
	cmd := os.Args[1]
	raw, err := os.ReadFile(os.Args[2])
	if err != nil {
		fatal("%v", err)
	}
	switch cmd {
	case "canon":
		var m updater.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			fatal("%v", err)
		}
		os.Stdout.Write(updater.CanonicalManifestBytes(&m))
	case "field":
		if len(os.Args) < 4 {
			fatal("field requires EXPR")
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			fatal("%v", err)
		}
		expr := os.Args[3]
		switch expr {
		case ".version":
			fmt.Print(asString(m["version"]))
		case ".arch":
			fmt.Print(asString(m["arch"]))
		case ".signature // empty":
			fmt.Print(asString(m["signature"]))
		default:
			if contains(expr, "nyxveilctl") {
				assets, _ := m["assets"].([]any)
				for _, a := range assets {
					am, _ := a.(map[string]any)
					name := asString(am["name"])
					if name == "nyxveilctl" || name == "ctl" {
						if contains(expr, ".sha256") {
							fmt.Print(asString(am["sha256"]))
						} else if contains(expr, ".url") {
							fmt.Print(asString(am["url"]))
						} else {
							fmt.Print(name)
						}
						return
					}
				}
				return
			}
			fatal("unsupported expr %q", expr)
		}
	default:
		fatal("unknown command %q", cmd)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && (func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})()))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "manifest-tool: "+format+"\n", args...)
	os.Exit(1)
}
