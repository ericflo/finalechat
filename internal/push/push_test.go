package push

import (
	"encoding/json"
	"strings"
	"testing"
)

// A question with many long options must still fit the one record a push
// service accepts: the option actions go first, then the body is shortened.
func TestEncodeFitsOneRecord(t *testing.T) {
	options := make([]string, 20)
	for i := range options {
		options[i] = strings.Repeat("x", 200)
	}
	n := Notification{
		Type: "question", Title: strings.Repeat("t", 200), Body: strings.Repeat("b", 5000),
		URL: "https://www.finalechat.com/t/01a07e1e-0000-7000-8000-000000000000?q=01a07e1e-0000-7000-8000-000000000001",
		Tag: "question:01a07e1e-0000-7000-8000-000000000001", ThreadID: "01a07e1e-0000-7000-8000-000000000000",
		QuestionID: "01a07e1e-0000-7000-8000-000000000001", Options: options, Important: true,
	}
	payload, err := encode(n)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > maxPayload {
		t.Fatalf("payload is %d bytes, over the %d-byte record", len(payload), maxPayload)
	}
	var back Notification
	if err := json.Unmarshal(payload, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Options) != 0 || back.QuestionID != n.QuestionID || back.URL != n.URL || back.Body == "" {
		t.Fatalf("shed the wrong fields: %+v", back)
	}

	small := Notification{Type: "question", Title: "deploy", Body: "Ship it?", URL: "https://x/t/1", Tag: "q:1", Options: []string{"Yes", "No", "Later"}}
	payload, err = encode(small)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(payload, &back)
	if len(back.Options) != 3 || back.Body != "Ship it?" {
		t.Fatalf("a small notification must be untouched: %+v", back)
	}
}
