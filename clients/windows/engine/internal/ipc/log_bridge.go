package ipc

import "github.com/nyxveil/client-windows/internal/diag"

// LogLineFromDiag converts a diag.Event into an IPC LogLine.
func LogLineFromDiag(e diag.Event) LogLine {
	return LogLine{
		Time:      e.Time.Local().Format("2006-01-02 15:04:05.000"),
		Level:     string(e.Level),
		Component: e.Component,
		Event:     e.Event,
		Message:   e.Message,
		Line:      e.Format(),
	}
}

// LogsSnapshotFromRing builds a backlog frame.
func LogsSnapshotFromRing(id string, events []diag.Event) LogsSnapshot {
	entries := make([]LogLine, 0, len(events))
	for _, e := range events {
		entries = append(entries, LogLineFromDiag(e))
	}
	return LogsSnapshot{
		Envelope: Envelope{Version: ProtocolVersion, Type: TypeLogsSnapshot, ID: id},
		Entries:  entries,
	}
}

// LogEventFromDiag builds a live push frame.
func LogEventFromDiag(e diag.Event) LogEventMessage {
	return LogEventMessage{
		Envelope: Envelope{Version: ProtocolVersion, Type: TypeLogEvent},
		Entry:    LogLineFromDiag(e),
	}
}
