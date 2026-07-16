package appapi

import (
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
)

// TestSinkEventName asserts the full chat:* taxonomy mapping. The wailsSink
// integration (actual EventsEmit) needs a live Wails runtime, so the mapping
// logic is verified here at the unit level instead.
func TestSinkEventName(t *testing.T) {
	cases := map[chat.SinkEventKind]string{
		chat.SinkRunStarted:     "chat:run_started",
		chat.SinkGroundingReady: "chat:grounding_ready",
		chat.SinkToken:          "chat:token_v2",
		chat.SinkToolCall:       "chat:tool_call",
		chat.SinkToolResult:     "chat:tool_result",
		chat.SinkRunCompleted:   "chat:run_completed",
		chat.SinkRunErrored:     "chat:run_errored",
		chat.SinkRunCancelled:   "chat:run_cancelled",
		chat.SinkUsage:          "chat:usage",
	}
	for kind, want := range cases {
		if got := sinkEventName(kind); got != want {
			t.Errorf("sinkEventName(%s) = %q, want %q", kind, got, want)
		}
	}
	if got := sinkEventName(chat.SinkEventKind("bogus")); got != "" {
		t.Errorf("unknown kind should map to empty, got %q", got)
	}
	if got := sinkEventName(chat.SinkImage); got != "chat:image" {
		t.Errorf("sinkEventName(SinkImage) = %q, want chat:image", got)
	}
}
