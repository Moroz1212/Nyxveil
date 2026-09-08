package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const nyxveilServiceUnit = "nyxveil-server"

type commandStateFile struct {
	Executed      []string              `json:"executed"`
	RebootPending []rebootPendingRecord `json:"reboot_pending,omitempty"`
}

type rebootPendingRecord struct {
	CommandID string `json:"command_id"`
	PreBootID string `json:"pre_boot_id"`
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
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *commandDedupeStore) containsExecuted(id string) bool {
	for _, existing := range s.data.Executed {
		if existing == id {
			return true
		}
	}
	return false
}

// TryMarkExecuted records a command as executed once. Returns false when already recorded.
func (s *commandDedupeStore) TryMarkExecuted(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.containsExecuted(id) {
		return false
	}
	s.data.Executed = append(s.data.Executed, id)
	_ = s.saveLocked()
	return true
}

func (s *commandDedupeStore) addRebootPending(commandID, preBootID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rec := range s.data.RebootPending {
		if rec.CommandID == commandID {
			s.data.RebootPending[i].PreBootID = preBootID
			_ = s.saveLocked()
			return
		}
	}
	s.data.RebootPending = append(s.data.RebootPending, rebootPendingRecord{
		CommandID: commandID,
		PreBootID: preBootID,
	})
	_ = s.saveLocked()
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
