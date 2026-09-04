package progress

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestWriteJSONLUsesCompactEnvelope(t *testing.T) {
	var output bytes.Buffer
	err := WriteJSONL(&output, Event{
		RequestID: "case",
		Type:      TypeProgress,
		Step:      123,
		MaxStep:   15000,
	})
	if err != nil {
		t.Fatal(err)
	}
	line := output.String()
	if strings.Contains(line, "request_id") || strings.Contains(line, "schema_version") {
		t.Fatalf("progress envelope is not compact: %q", line)
	}
	if len(line) > 64 {
		t.Fatalf("representative progress event is unexpectedly large: %d bytes", len(line))
	}
	var event Event
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Version != Version || event.RequestID != "case" || event.Type != TypeProgress {
		t.Fatalf("unexpected round trip: %+v", event)
	}
}

func BenchmarkWriteJSONL(b *testing.B) {
	event := Event{
		RequestID: "case",
		Type:      TypeProgress,
		Step:      123,
		MaxStep:   15000,
	}
	for b.Loop() {
		if err := WriteJSONL(io.Discard, event); err != nil {
			b.Fatal(err)
		}
	}
}
