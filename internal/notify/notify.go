// Package notify pushes task results to an external messaging endpoint.
//
// Four payload dialects are supported so operators can point the webhook at
// whatever push service they already run without standing up a transformer:
//
//	json     generic POST {"title","text","status","job","run_id"}
//	bark     POST <base>/push {"title","body","group"}   (Bark / iOS)
//	wecom    POST {"msgtype":"text","text":{"content"}}  (企业微信机器人)
//	telegram POST {"chat_id","text"}                      (Bot API sendMessage)
//
// Delivery is best-effort by design: the engine fires it in the background
// with a short timeout and logs failures without affecting the run record.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
)

// Message is one task outcome ready for delivery.
type Message struct {
	Title  string // one-liner, e.g. "备份成功：Docker备份"
	Body   string // detail lines
	Status string // engine status string (success/partial/failed)
	Job    string
	RunID  string
}

// Deliver posts msg to the configured endpoint. It is synchronous on purpose
// (the engine calls it from a goroutine) and bounded by a 15s client timeout.
func Deliver(ctx context.Context, cfg config.NotifyConfig, msg Message) error {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil
	}
	payload, endpoint, err := buildPayload(cfg, msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "qzrs-webdav-backup/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %s", resp.Status)
	}
	return nil
}

func buildPayload(cfg config.NotifyConfig, msg Message) ([]byte, string, error) {
	text := msg.Title
	if msg.Body != "" {
		text = msg.Title + "\n" + msg.Body
	}
	switch cfg.EffectiveFormat() {
	case "json":
		return marshal(map[string]string{
			"title": msg.Title, "text": text, "status": msg.Status,
			"job": msg.Job, "run_id": msg.RunID,
		}), strings.TrimSpace(cfg.URL), nil
	case "bark":
		return marshal(map[string]string{
			"title": msg.Title, "body": msg.Body, "group": "qzrs-webdav-backup",
		}), strings.TrimRight(strings.TrimSpace(cfg.URL), "/") + "/push", nil
	case "wecom":
		return marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		}), strings.TrimSpace(cfg.URL), nil
	case "telegram":
		return marshal(map[string]string{
			"chat_id": cfg.ChatID, "text": text,
		}), strings.TrimSpace(cfg.URL), nil
	default:
		return nil, "", fmt.Errorf("unknown notify format %q", cfg.Format)
	}
}

func marshal(v any) []byte {
	raw, _ := json.Marshal(v) // plain maps/strings cannot fail
	return raw
}
