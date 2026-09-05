package main

import (
	"bufio"
	"context"
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
	configPath := flag.String("config", paths.ServerConfig(), "path to server.json")
	register := flag.String("register", "", "bootstrap token (prefer --register-stdin); registers then exits")
	registerStdin := flag.Bool("register-stdin", false, "read bootstrap token from stdin once, scrub, register, exit")
	skipTUN := flag.Bool("skip-tun", false, "skip TUN/datapath (explicit only; required on non-Linux)")
	testMode := flag.Bool("test-mode", false, "allow register without public_host")
	controlHTTP := flag.String("control-http", "", "loopback HTTP control addr (Windows/tests); empty uses unix socket on Linux")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("nyxveil-server %s (core %s, %s)\n", version.ServerVersion, version.CoreVersion, version.ProtocolVersion)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = node.Shutdown(shutdownCtx)
	log.Println("shutdown complete")
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
		return err
	}
	fmt.Printf("registered node_id=%s config_version=%d\n", resp.NodeID, resp.ConfigVersion)
	return nil
}
