package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestWebSocketHandshake(t *testing.T) {
	srv := newServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var hello wsMessage
	if err := wsjson.Read(ctx, c, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != "hello" {
		t.Fatalf("expected hello, got %q", hello.Type)
	}
	// A status frame should arrive within a few ticks.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel2()
	for {
		var msg wsMessage
		if err := wsjson.Read(ctx2, c, &msg); err != nil {
			t.Fatalf("expected a status frame: %v", err)
		}
		if msg.Type == "status" {
			return
		}
	}
}
