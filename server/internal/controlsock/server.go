// Package controlsock exposes a local JSON-RPC control channel for nyxveilctl.
package controlsock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// StatusFunc returns the current node status payload.
type StatusFunc func() any

// HealthFunc returns a lightweight health payload.
type HealthFunc func() any

// Server serves status/health over a Unix domain socket (Linux) or
// loopback HTTP (Windows / when socket path is empty).
type Server struct {
	SocketPath string
	HTTPAddr   string // used on Windows or as test fallback; e.g. 127.0.0.1:0
	Status     StatusFunc
	Health     HealthFunc

	mu         sync.Mutex
	ln         net.Listener
	httpServer *http.Server
	addr       string
}

type rpcRequest struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Start begins serving. On Linux with SocketPath set, uses a Unix socket.
// On Windows (or empty SocketPath), binds HTTP to HTTPAddr (default 127.0.0.1:0).
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil || s.httpServer != nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleHTTPStatus)
	mux.HandleFunc("/health", s.handleHTTPHealth)
	mux.HandleFunc("/", s.handleRPC)

	useUnix := runtime.GOOS != "windows" && s.SocketPath != ""
	if useUnix {
		if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0o755); err != nil {
			return err
		}
		_ = os.Remove(s.SocketPath)
		ln, err := net.Listen("unix", s.SocketPath)
		if err != nil {
			return err
		}
		_ = os.Chmod(s.SocketPath, 0o660)
		s.ln = ln
		s.addr = s.SocketPath
		s.httpServer = &http.Server{Handler: mux}
		go func() {
			_ = s.httpServer.Serve(ln)
		}()
		go func() {
			<-ctx.Done()
			_ = s.Stop()
		}()
		return nil
	}

	addr := s.HTTPAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	s.httpServer = &http.Server{Handler: mux}
	go func() {
		_ = s.httpServer.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		_ = s.Stop()
	}()
	return nil
}

// Addr returns the listen address (socket path or host:port).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Stop closes the listener.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.httpServer != nil {
		err = s.httpServer.Close()
		s.httpServer = nil
	}
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
	if runtime.GOOS != "windows" && s.SocketPath != "" {
		_ = os.Remove(s.SocketPath)
	}
	return err
}

func (s *Server) handleHTTPStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.statusResult())
}

func (s *Server) handleHTTPHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.healthResult())
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, rpcResponse{Error: "invalid json"})
		return
	}
	resp := rpcResponse{ID: req.ID}
	switch req.Method {
	case "status":
		resp.Result = s.statusResult()
	case "health":
		resp.Result = s.healthResult()
	default:
		resp.Error = fmt.Sprintf("unknown method %q", req.Method)
	}
	writeJSON(w, resp)
}

func (s *Server) statusResult() any {
	if s.Status != nil {
		return s.Status()
	}
	return map[string]any{"running": false}
}

func (s *Server) healthResult() any {
	if s.Health != nil {
		return s.Health()
	}
	return map[string]any{"healthy": false}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// DialHTTP is a helper for tests / Windows ctl against the loopback HTTP server.
func DialHTTP(baseURL, path string) (*http.Response, error) {
	if baseURL == "" {
		return nil, errors.New("controlsock: empty base URL")
	}
	return http.Get(baseURL + path)
}
