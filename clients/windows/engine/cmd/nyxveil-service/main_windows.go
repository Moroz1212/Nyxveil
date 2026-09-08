//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/state"
	"github.com/nyxveil/client-windows/internal/ticketbroker"
	"golang.org/x/sys/windows/svc"
)

const (
	serviceName     = "NyxveilClientService"
	clientVer       = "1.1.2"
	coreVer         = "1.0.0"
	ticketWaitLimit = 2 * time.Minute
)

func main() {
	console := flag.Bool("console", false, "run as console process (named pipe server; for tests)")
	pipeName := flag.String("pipe", ipc.PipeName, "named pipe path")
	caFile := flag.String("ca-file", "", "optional private CA PEM for node TLS (else system trust)")
	provisionSID := flag.Bool("provision-sid", false, "write authorized interactive user SID for LocalSystem pipe ACL")
	writeSIDToken := flag.String("write-sid-token", "", "non-elevated: write current user SID to path for installer")
	installSIDToken := flag.String("install-sid-from-token", "", "elevated: install SID from token into protected ProgramData")
	lockSID := flag.Bool("lock-sid-acl", false, "elevated: protect Client data dir + authorized-user.sid ACL")
	protectDataDir := flag.Bool("protect-client-data-dir", false, "elevated: harden ProgramData\\Nyxveil\\Client ACL")
	sidArg := flag.String("sid", "", "explicit SID for -provision-sid (default: current user)")
	finalizeSCM := flag.Bool("finalize-scm", false, "elevated install: create/configure/start service + pipe ready; rollback on fail")
	failAfter := flag.String("finalize-scm-fail-after", "", "test: create|description|failure|start|ready")
	uninstallRecover := flag.Bool("uninstall-network-cleanup", false, "elevated uninstall: recover journal before ProgramData delete")
	gateWintun := flag.Bool("gate-wintun", false, "elevated gate: open/close temporary Wintun adapter")
	gateNet := flag.Bool("gate-net-tx", false, "elevated gate: isolated route/DNS transaction")
	gateFullTunnel := flag.Bool("gate-full-tunnel", false, "elevated gate: production 0.0.0.0/1+128.0.0.0/1 on Wintun")
	gateIPv6 := flag.Bool("gate-ipv6", false, "elevated gate: IPv6 mutate/restore round-trip")
	gateCrash := flag.String("gate-crash-after", "", "elevated gate: apply step then exit 99 (bypass|tun_addr|tun_dns|ipv6|default_vpn)")
	gateClean := flag.Bool("gate-verify-clean", false, "elevated gate: RecoverOnStartup + assert no stale gate routes")
	flag.Parse()

	if *writeSIDToken != "" {
		if err := ipc.WriteSIDToken(*writeSIDToken); err != nil {
			log.Fatalf("write-sid-token: %v", err)
		}
		fmt.Println("SID token written:", *writeSIDToken)
		return
	}
	if *installSIDToken != "" {
		if err := ipc.InstallAuthorizedSIDFromToken(*installSIDToken); err != nil {
			log.Fatalf("install-sid-from-token: %v", err)
		}
		fmt.Println("authorized SID installed from token")
		return
	}
	if *protectDataDir {
		if err := ipc.ProtectClientDataDir(); err != nil {
			log.Fatalf("protect-client-data-dir: %v", err)
		}
		fmt.Println("client data dir protected:", ipc.ClientDataDir())
		return
	}
	if *provisionSID {
		if err := ipc.ProvisionAuthorizedSID(*sidArg); err != nil {
			log.Fatalf("provision-sid: %v", err)
		}
		fmt.Println("authorized SID provisioned:", ipc.AuthorizedSIDPath())
		return
	}
	if *lockSID {
		if err := ipc.LockAuthorizedSIDACL(); err != nil {
			log.Fatalf("lock-sid-acl: %v", err)
		}
		fmt.Println("authorized SID ACL locked:", ipc.AuthorizedSIDPath())
		return
	}
	if *finalizeSCM {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("finalize-scm: %v", err)
		}
		if err := engine.FinalizeSCMInstall(exe, *failAfter); err != nil {
			log.Fatalf("finalize-scm: %v", err)
		}
		fmt.Println("SCM finalize OK")
		return
	}
	if *uninstallRecover {
		if err := engine.UninstallNetworkCleanup(); err != nil {
			log.Fatalf("uninstall-network-cleanup: %v", err)
		}
		fmt.Println("uninstall network cleanup OK")
		return
	}
	if *gateWintun {
		if err := engine.RunGateWintunOpen(); err != nil {
			log.Fatalf("gate-wintun: %v", err)
		}
		return
	}
	if *gateNet {
		if err := engine.RunGateNetTransaction(); err != nil {
			log.Fatalf("gate-net-tx: %v", err)
		}
		return
	}
	if *gateFullTunnel {
		if err := engine.RunGateFullTunnelRoutes(); err != nil {
			log.Fatalf("gate-full-tunnel: %v", err)
		}
		return
	}
	if *gateIPv6 {
		if err := engine.RunGateIPv6RoundTrip(); err != nil {
			log.Fatalf("gate-ipv6: %v", err)
		}
		return
	}
	if *gateCrash != "" {
		if err := engine.RunGateCrashStep(*gateCrash); err != nil {
			log.Fatalf("gate-crash-after: %v", err)
		}
		return
	}
	if *gateClean {
		if err := engine.VerifyGateClean(); err != nil {
			log.Fatalf("gate-verify-clean: %v", err)
		}
		return
	}

	isSvc, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("svc detect: %v", err)
	}
	if isSvc && !*console {
		err = svc.Run(serviceName, &nyxveilService{pipeName: *pipeName, caFile: *caFile})
		if err != nil {
			log.Fatalf("service: %v", err)
		}
		return
	}
	// Console/dev: auto-provision current user SID if missing so ListenSecure works.
	if _, err := os.Stat(ipc.AuthorizedSIDPath()); err != nil {
		if err := ipc.ProvisionAuthorizedSID(""); err != nil {
			log.Printf("warn: could not auto-provision SID: %v", err)
		}
	}
	if err := runConsole(context.Background(), *pipeName, *caFile); err != nil {
		log.Fatalf("console: %v", err)
	}
}

type nyxveilService struct {
	pipeName string
	caFile   string
}

func (s *nyxveilService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime, err := newServiceRuntime(s.pipeName, s.caFile)
	if err != nil {
		log.Printf("runtime: %v", err)
		return false, 1
	}
	errCh := make(chan error, 1)
	go func() { errCh <- runtime.serve(ctx) }()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	for {
		select {
		case err := <-errCh:
			cleanErr := runtime.shutdown()
			if cleanErr != nil {
				log.Printf("pipe exit: network restore FAILED: %v", cleanErr)
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("pipe server: %v", err)
				return false, 1
			}
			if cleanErr != nil {
				return false, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				// Synchronous network restore BEFORE service reports stopped.
				cleanErr := runtime.shutdown()
				cancel()
				select {
				case <-errCh:
				case <-time.After(15 * time.Second):
				}
				if cleanErr != nil {
					log.Printf("service stop: network restore FAILED (journal preserved): %v", cleanErr)
					return false, 1
				}
				return false, 0
			case svc.PowerEvent:
				// PBT_APMRESUMEAUTOMATIC=18, PBT_APMRESUMESUSPEND=7
				if c.EventType == 18 || c.EventType == 7 {
					runtime.onPowerResume()
				}
			default:
				log.Printf("unexpected service control: %d", c.Cmd)
			}
		}
	}
}

type serviceRuntime struct {
	mgr       *engine.Manager
	broker    *ticketbroker.Broker
	ln        net.Listener
	pipe      string
	lifecycle *engine.LifecycleMonitor
}

type pipeClient struct {
	ch       chan []byte
	wantLogs atomic.Bool
}

func newServiceRuntime(pipeName, caFile string) (*serviceRuntime, error) {
	_ = diag.MustInitFile()
	diag.Info("SERVICE", "start", "Nyxveil.Service starting ver="+clientVer)

	trust := engine.NewSystemTrust()
	if caFile != "" {
		t, err := engine.NewTrustWithCAFile(caFile)
		if err != nil {
			return nil, err
		}
		trust = t
	}
	broker := ticketbroker.New()
	mgr := engine.NewManager(engine.Options{
		Trust:  trust,
		Routes: engine.NewWindowsApplier(),
		TUN:    engine.NewTunFactory(),
	})
	if ra, ok := mgr.Routes.(*engine.WindowsApplier); ok {
		if err := ra.RecoverOnStartup(); err != nil {
			return nil, fmt.Errorf("route recovery required before service start: %w", err)
		}
	}
	lc := engine.NewLifecycleMonitor(mgr)
	lc.Start()
	rt := &serviceRuntime{mgr: mgr, broker: broker, pipe: pipeName, lifecycle: lc}
	return rt, nil
}

func (rt *serviceRuntime) onPowerResume() {
	if rt.lifecycle != nil {
		rt.lifecycle.OnPowerResume()
	}
}

func (rt *serviceRuntime) shutdown() error {
	if rt.lifecycle != nil {
		rt.lifecycle.Stop()
	}
	rt.broker.CancelAll()
	err := rt.mgr.Disconnect(context.Background())
	if rt.ln != nil {
		_ = rt.ln.Close()
	}
	if err != nil {
		log.Printf("Disconnect restore failure (journal must remain): %v", err)
	}
	return err
}

func (rt *serviceRuntime) serve(ctx context.Context) error {
	var (
		writersMu sync.Mutex
		writers   = map[*pipeClient]struct{}{}
	)
	broadcast := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		line := append(b, '\n')
		writersMu.Lock()
		defer writersMu.Unlock()
		for pc := range writers {
			select {
			case pc.ch <- append([]byte(nil), line...):
			default:
			}
		}
	}
	broadcastLogs := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		line := append(b, '\n')
		writersMu.Lock()
		defer writersMu.Unlock()
		for pc := range writers {
			if !pc.wantLogs.Load() {
				continue
			}
			select {
			case pc.ch <- append([]byte(nil), line...):
			default:
			}
		}
	}
	diag.Default().SetHook(func(e diag.Event) {
		broadcastLogs(ipc.LogEventFromDiag(e))
	})
	rt.mgr.OnStatusChange = func() {
		broadcast(rt.mgr.StatusSnapshot(clientVer, coreVer))
	}

	// Read-only telemetry push while Connected so GUI can compute TX/RX rates.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st, _ := rt.mgr.State.Get()
				if st == state.Connected {
					broadcast(rt.mgr.StatusSnapshot(clientVer, coreVer))
				}
			}
		}
	}()

	rt.mgr.RequestTicket = func(reqCtx context.Context, need ipc.NeedAccessTicket) (string, error) {
		st, _ := rt.mgr.State.Get()
		diag.InfoFields("RECONNECT", "need_ticket", map[string]string{
			"location": need.DesiredLocationID,
			"reason":   need.Reason,
			"state":    st.String(),
		})
		req, err := rt.broker.Begin(need.DesiredLocationID, need.Reason, st.String())
		if err != nil {
			return "", err
		}
		msg := ipc.NeedAccessTicket{
			Envelope:          ipc.Envelope{Version: ipc.ProtocolVersion, Type: ipc.TypeNeedAccessTicket, ID: req.RequestID},
			RequestID:         req.RequestID,
			DesiredLocationID: req.LocationID,
			Reason:            req.Reason,
			State:             req.State,
		}
		broadcast(msg)
		return rt.broker.Wait(reqCtx, req, ticketWaitLimit)
	}

	ln, err := ipc.ListenSecure(rt.pipe)
	if err != nil {
		return err
	}
	rt.ln = ln
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	diag.Info("IPC", "listen", "pipe="+rt.pipe)
	log.Printf("listening on %s service=%s", rt.pipe, serviceName)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		diag.Info("IPC", "accept", "client connected")
		go handleConn(ctx, conn, rt.mgr, rt.broker, &writersMu, writers)
	}
}

func runConsole(parent context.Context, pipeName, caFile string) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	rt, err := newServiceRuntime(pipeName, caFile)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		rt.shutdown()
	}()
	log.Printf("Nyxveil.Service console mode pipe=%s", pipeName)
	return rt.serve(ctx)
}

func handleConn(
	ctx context.Context,
	conn net.Conn,
	mgr *engine.Manager,
	broker *ticketbroker.Broker,
	writersMu *sync.Mutex,
	writers map[*pipeClient]struct{},
) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	var writeMu sync.Mutex

	writeJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
		return w.Flush()
	}

	pc := &pipeClient{ch: make(chan []byte, 64)}
	writersMu.Lock()
	writers[pc] = struct{}{}
	writersMu.Unlock()
	defer func() {
		writersMu.Lock()
		delete(writers, pc)
		writersMu.Unlock()
		close(pc.ch)
		diag.Info("IPC", "disconnect", "client gone")
	}()

	go func() {
		for line := range pc.ch {
			writeMu.Lock()
			_, _ = w.Write(line)
			_ = w.Flush()
			writeMu.Unlock()
		}
	}()

	_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			log.Printf("read: %v", err)
			return
		}
		var env ipc.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": ipc.TypeError, "error": "bad envelope"})
			continue
		}
		diag.InfoFields("IPC", "request", map[string]string{"type": env.Type, "id": env.ID})
		switch env.Type {
		case ipc.TypeHello, ipc.TypeStatus:
			_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))
		case ipc.TypeGetLogs:
			snap := ipc.LogsSnapshotFromRing(env.ID, diag.Default().Ring().Snapshot())
			_ = writeJSON(snap)
		case ipc.TypeSubscribeLogs:
			pc.wantLogs.Store(true)
			snap := ipc.LogsSnapshotFromRing(env.ID, diag.Default().Ring().Snapshot())
			_ = writeJSON(snap)
			diag.Info("IPC", "subscribe_logs", "live=on")
		case ipc.TypeUnsubscribeLogs:
			pc.wantLogs.Store(false)
			diag.Info("IPC", "unsubscribe_logs", "live=off")
			_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": "ok", "id": env.ID})
		case ipc.TypeConnect:
			var req ipc.ConnectRequest
			if err := json.Unmarshal(line, &req); err != nil {
				_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": ipc.TypeError, "error": err.Error()})
				continue
			}
			diag.InfoFields("SESSION", "Connect requested", map[string]string{
				"location": req.DesiredLocationID,
				"cp_host":  req.ControlPlaneHost,
			})
			creq := engine.ConnectRequest{
				DesiredLocationID: req.DesiredLocationID,
				AccessTicket:      req.AccessTicket,
				SignedCatalogJSON: req.SignedCatalogJSON,
				CatalogKeys:       req.CatalogKeys,
				DevicePrivateKey:  req.DevicePrivateKey,
				ControlPlaneHost:  req.ControlPlaneHost,
			}
			if err := mgr.Connect(ctx, creq); err != nil {
				diag.Error("SESSION", "Connect failed", err.Error())
				_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": ipc.TypeError, "id": env.ID, "error": err.Error()})
			}
			_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))
		case ipc.TypeDisconnect:
			diag.Info("SESSION", "Disconnect requested", "")
			broker.CancelAll()
			_ = mgr.Disconnect(ctx)
			_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))
		case ipc.TypeProvideAccessTicket, ipc.TypeAccessTicket:
			var resp ipc.ProvideAccessTicket
			if err := json.Unmarshal(line, &resp); err != nil {
				_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": ipc.TypeError, "error": "bad provide_access_ticket"})
				continue
			}
			if resp.RequestID == "" {
				resp.RequestID = env.ID
			}
			diag.InfoFields("IPC", "provide_ticket", map[string]string{"request_id": resp.RequestID})
			if err := broker.Provide(resp.RequestID, resp.AccessTicket); err != nil {
				_ = writeJSON(map[string]any{
					"v": ipc.ProtocolVersion, "type": ipc.TypeError,
					"error": fmt.Sprintf("provide_access_ticket rejected: %v", err),
				})
			}
		case ipc.TypeCancel:
			diag.Info("SESSION", "Cancel", "")
			broker.CancelAll()
			_ = mgr.Disconnect(ctx)
			_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))
		case ipc.TypeGateApplyIsolated:
			pfx, via, err := mgr.ApplyGateIsolatedTransaction()
			if err != nil {
				_ = writeJSON(map[string]any{"v": ipc.ProtocolVersion, "type": ipc.TypeError, "error": err.Error()})
				continue
			}
			_ = writeJSON(map[string]any{
				"v": ipc.ProtocolVersion, "type": "gate_applied",
				"id": env.ID, "request_id": env.ID,
				"dest": pfx.String(), "via": via.String(),
			})
		case ipc.TypeGateClear:
			_ = mgr.Disconnect(ctx)
			_ = writeJSON(mgr.StatusSnapshot(clientVer, coreVer))
		default:
			_ = writeJSON(map[string]any{
				"v": ipc.ProtocolVersion, "type": ipc.TypeError,
				"error": fmt.Sprintf("unknown type %q", env.Type),
			})
		}
	}
}
