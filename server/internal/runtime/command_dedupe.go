package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/nyxveil/server/internal/filemeta"
)

const nyxveilServiceUnit = "nyxveil-server"

type commandStateFile struct {
	Executed       []string               `json:"executed"`
	RebootPending  []rebootPendingRecord  `json:"reboot_pending,omitempty"`
	RestartPending []restartPendingRecord `json:"restart_pending,omitempty"`
	PendingResults []pendingResultRecord  `json:"pending_results,omitempty"`
}

type rebootPendingRecord struct {
	CommandID string `json:"command_id"`
	PreBootID string `json:"pre_boot_id"`
}

type restartPendingRecord struct {
	CommandID string `json:"command_id"`
	StartedAt string `json:"started_at,omitempty"`
}

type pendingResultRecord struct {
	CommandID     string `json:"command_id"`
	Success       bool   `json:"success"`
	ResultCode    string `json:"result_code"`
	ResultMessage string `json:"result_message"`
	BootID        string `json:"boot_id,omitempty"`
}

type commandDedupeStore struct {
	path string
	mu   sync.Mutex
	data commandStateFile
}

func newCommandDedupeStore(path string) *commandDedupeStore {
	s := &commandDedupeStore{path: path}
	s.load()
	return s
}

func (s *commandDedupeStore) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var data commandStateFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	if data.Executed == nil {
		data.Executed = []string{}
	}
	s.data = data
}

func (s *commandDedupeStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	// Crash-sensitive command journal — DurableWrite required.
	if err := filemeta.DurableWrite(s.path, raw, 0o600); err != nil {
		return err
	}
	uid, gid, _ := filemeta.LookupServiceIDs()
	_ = filemeta.ApplyOwnerMode(s.path, uid, gid, 0o600)
	return nil
}

func (s *commandDedupeStore) containsExecuted(id string) bool {
	for _, existing := range s.data.Executed {
		if existing == id {
			return true
		}
	}
	return false
}

// WasExecuted reports whether commandID was already recorded as executed.
func (s *commandDedupeStore) WasExecuted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.containsExecuted(id)
}

// TryMarkExecuted records a command as executed once. Returns false when already recorded.
// Returns false also when durable journal write fails (fail closed — do not execute).
func (s *commandDedupeStore) TryMarkExecuted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.containsExecuted(id) {
		return false
	}
	s.data.Executed = append(s.data.Executed, id)
	if err := s.saveLocked(); err != nil {
		s.data.Executed = s.data.Executed[:len(s.data.Executed)-1]
		return false
	}
	return true
}

func (s *commandDedupeStore) addRebootPending(commandID, preBootID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rec := range s.data.RebootPending {
		if rec.CommandID == commandID {
			s.data.RebootPending[i].PreBootID = preBootID
			return s.saveLocked()
		}
	}
	s.data.RebootPending = append(s.data.RebootPending, rebootPendingRecord{
		CommandID: commandID,
		PreBootID: preBootID,
	})
	return s.saveLocked()
}

func (s *commandDedupeStore) removeRebootPending(commandID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data.RebootPending[:0]
	for _, rec := range s.data.RebootPending {
		if rec.CommandID != commandID {
			out = append(out, rec)
		}
	}
	s.data.RebootPending = out
	_ = s.saveLocked()
}

func (s *commandDedupeStore) rebootPending() []rebootPendingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rebootPendingRecord, len(s.data.RebootPending))
	copy(out, s.data.RebootPending)
	return out
}

func (s *commandDedupeStore) addRestartPending(commandID, startedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rec := range s.data.RestartPending {
		if rec.CommandID == commandID {
			s.data.RestartPending[i].StartedAt = startedAt
			return s.saveLocked()
		}
	}
	s.data.RestartPending = append(s.data.RestartPending, restartPendingRecord{
		CommandID: commandID,
		StartedAt: startedAt,
	})
	return s.saveLocked()
}

func (s *commandDedupeStore) removeRestartPending(commandID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data.RestartPending[:0]
	for _, rec := range s.data.RestartPending {
		if rec.CommandID != commandID {
			out = append(out, rec)
		}
	}
	s.data.RestartPending = out
	_ = s.saveLocked()
}

func (s *commandDedupeStore) restartPending() []restartPendingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]restartPendingRecord, len(s.data.RestartPending))
	copy(out, s.data.RestartPending)
	return out
}

func (s *commandDedupeStore) addPendingResult(rec pendingResultRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.data.PendingResults {
		if existing.CommandID == rec.CommandID {
			s.data.PendingResults[i] = rec
			return s.saveLocked()
		}
	}
	s.data.PendingResults = append(s.data.PendingResults, rec)
	return s.saveLocked()
}

func (s *commandDedupeStore) removePendingResult(commandID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data.PendingResults[:0]
	for _, rec := range s.data.PendingResults {
		if rec.CommandID != commandID {
			out = append(out, rec)
		}
	}
	s.data.PendingResults = out
	_ = s.saveLocked()
}

func (s *commandDedupeStore) pendingResults() []pendingResultRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pendingResultRecord, len(s.data.PendingResults))
	copy(out, s.data.PendingResults)
	return out
}
