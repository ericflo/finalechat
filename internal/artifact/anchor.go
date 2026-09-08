package artifact

import (
	"encoding/json"
	"fmt"
	"unicode"
)

// SourceAnchor is a presentation hint within a particular native dataset.
// It grants no access and does not assert that a record has been captured.
type SourceAnchor struct {
	DatasetFormat string `json:"dataset_format"`
	SessionID     string `json:"session_id"`
	Seq           int64  `json:"seq,omitempty"`
	EventID       string `json:"event_id,omitempty"`
	File          string `json:"file,omitempty"`
	Line          int64  `json:"line,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
}

func (a SourceAnchor) Validate(m Manifest) error {
	for _, value := range []string{a.DatasetFormat, a.SessionID, a.EventID, a.MessageID} {
		if len(value) > 200 {
			return fmt.Errorf("anchor identifiers must be within 200 bytes")
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return fmt.Errorf("anchor identifiers cannot contain control characters")
			}
		}
	}
	if a.DatasetFormat == "" || a.SessionID == "" || a.DatasetFormat != m.Dataset["format"] || a.SessionID != m.Dataset["session_id"] {
		return fmt.Errorf("anchor belongs to another dataset")
	}
	selectors := 0
	if a.Seq != 0 {
		if a.Seq < 1 || a.Seq > 9007199254740991 {
			return fmt.Errorf("invalid event sequence")
		}
		selectors++
	}
	if a.EventID != "" {
		selectors++
	}
	if a.MessageID != "" {
		selectors++
	}
	if a.File != "" || a.Line != 0 {
		file, ok := m.Find(a.File)
		if !ok || file.Role != "source" || a.Line < 1 || a.Line > file.Size {
			return fmt.Errorf("anchor must name a line in a source file in this revision")
		}
		selectors++
	}
	if selectors != 1 {
		return fmt.Errorf("anchor requires exactly one sequence, event ID, message ID or file/line")
	}
	return nil
}

func (a SourceAnchor) Metadata() map[string]any {
	raw, _ := json.Marshal(a)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
