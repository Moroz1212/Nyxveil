package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/runtime"
	"github.com/nyxveil/server/internal/version"
)

func main() {
	// Subcommand form used by ctl/gate probes: `nyxveil-server version [--json]`.
	// Must be handled before flag.Parse so "version" is never treated as a
	// daemon start (which caused live failed_gate=server_version).
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		printServerVersion(os.Stdout, os.Args[2:])
		return
	}

	configPath := flag.String("config", paths.ServerConfig(), "path to server.json")
	register := flag.String("register", "", "bootstrap token (prefer --register-stdin); registers then exits")
	registerStdin := flag.Bool("register-stdin", false, "read bootstrap token from stdin once, scrub, register, exit")
	skipTUN := flag.Bool("skip-tun", false, "skip TUN/datapath (explicit only; required on non-Linux)")
	testMode := flag.Bool("test-mode", false, "allow register without public_host")
	controlHTTP := flag.String("control-http", "", "loopback HTTP control addr (Windows/tests); empty uses unix socket on Linux")
	showVersion := flag.Bool("version", false, "print version and exit")
	versionJSON := flag.Bool("version-json", false, "print machine-readable version JSON and exit")
	flag.Parse()

	if *versionJSON {
		printServerVersionJSON(os.Stdout)
		return
	}
	if *showVersion {
		printServerVersionHuman(os.Stdout)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *registerStdin || *register != "" {
		token := *register
		if *registerStdin {
			var err error
			token, err = readTokenStdin()
			if err != nil {
				log.Fatal(err)
			}
		}
		err := doRegister(ctx, *configPath, token, *testMode)
		// Scrub token from memory as best-effort.
		token = strings.Repeat("\x00", len(token))
		_ = token
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	node, err := runtime.New(runtime.Options{
		ConfigPath:  *configPath,
		SkipTUN:     *skipTUN,
		TestMode:    *testMode,
		ControlHTTP: *controlHTTP,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := node.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("nyxveil-server %s running", version.ServerVersion)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = node.Shutdown(shutdownCtx)
	log.Println("shutdown complete")
}

func printServerVersion(w io.Writer, args []string) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "machine-readable JSON")
	_ = fs.Parse(args)
	if *asJSON {
		printServerVersionJSON(w)
		return
	}
	printServerVersionHuman(w)
}

func printServerVersionHuman(w io.Writer) {
	fmt.Fprintf(w, "nyxveil-server %s (core %s, %s)\n", version.ServerVersion, version.CoreVersion, version.ProtocolVersion)
}

func printServerVersionJSON(w io.Writer) {
	_ = json.NewEncoder(w).Encode(map[string]string{
		"server_version": version.ServerVersion,
		"core_version":   version.CoreVersion,
		"protocol":       version.ProtocolVersion,
	})
}

func readTokenStdin() (string, error) {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	// Empty token is allowed: Register uses PoP (NodeToken) when node.key already exists.
	return strings.TrimSpace(line), nil
}

func doRegister(ctx context.Context, configPath, token string, testMode bool) error {
	if _, err := localconfig.Load(configPath); err != nil {
		return err
	}
	node, err := runtime.New(runtime.Options{
		ConfigPath: configPath,
		SkipTUN:    true,
		TestMode:   testMode,
	})
	if err != nil {
		return err
	}
	resp, err := node.Register(ctx, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nyxveil-server: registration failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "nyxveil-server: if Control Plane returned success, local node.key must be kept for PoP retry — do not delete identity\n")
		return err
	}
	fmt.Printf("registered node_id=%s config_version=%d\n", resp.NodeID, resp.ConfigVersion)
	return nil
}
