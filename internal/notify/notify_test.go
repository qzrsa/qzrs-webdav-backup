package notify

import (
	"encoding/json"
	"testing"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
)

func TestBuildPayloadGotify(t *testing.T) {
	cfg := config.NotifyConfig{URL: "https://push.example.com", Format: "gotify", ChatID: "AbCd1234"}
	raw, endpoint, err := buildPayload(cfg, Message{Title: "T", Body: "B", Success: false})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://push.example.com/message?token=AbCd1234" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["priority"].(float64) != 8 {
		t.Fatalf("failure priority = %v, want 8", m["priority"])
	}

	// Success flips priority; full message endpoint is used verbatim.
	cfg.URL = "https://push.example.com/message?token=XYZ"
	_, endpoint, _ = buildPayload(cfg, Message{Title: "T", Success: true})
	if endpoint != cfg.URL {
		t.Fatalf("verbatim endpoint = %q", endpoint)
	}
	if raw[0] != '{' {
		t.Fatal("payload should be JSON")
	}
}

func TestBuildPayloadFormats(t *testing.T) {
	cases := []struct{ format, wantEndpoint string }{
		{"json", "https://hook.example/x"},
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=1"},
		{"telegram", "https://api.telegram.org/botT/sendMessage"},
	}
	for _, c := range cases {
		cfg := config.NotifyConfig{URL: c.wantEndpoint, Format: c.format, ChatID: "42"}
		raw, endpoint, err := buildPayload(cfg, Message{Title: "T", Body: "B"})
		if err != nil {
			t.Fatalf("%s: %v", c.format, err)
		}
		if endpoint != c.wantEndpoint {
			t.Fatalf("%s endpoint = %q", c.format, endpoint)
		}
		if len(raw) == 0 {
			t.Fatalf("%s empty payload", c.format)
		}
	}

	// bark appends /push to the personal base.
	cfg := config.NotifyConfig{URL: "https://api.day.app/key", Format: "bark"}
	_, endpoint, _ := buildPayload(cfg, Message{Title: "T"})
	if endpoint != "https://api.day.app/key/push" {
		t.Fatalf("bark endpoint = %q", endpoint)
	}
}
