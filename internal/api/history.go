package api

import (
	"context"
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/proto"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// cutAt is how much of a large tool payload the chat gets; History with full loads the rest.
const cutAt = 4096

// slim cuts a tool result's output and a tool call's long input strings to cutAt bytes, marking
// the event truncated. It returns e itself when nothing is cut, else a copy: broadcast events are shared.
func slim(e *bandav1.Event) *bandav1.Event {
	switch k := e.Kind.(type) {
	case *bandav1.Event_ToolResult:
		if len(k.ToolResult.Output) <= cutAt {
			return e
		}
		c := proto.Clone(e).(*bandav1.Event)
		c.GetToolResult().Output = cut(k.ToolResult.Output)
		c.Truncated = true
		return c
	case *bandav1.Event_ToolCall:
		if len(k.ToolCall.InputJson) <= cutAt {
			return e
		}
		var in any
		if json.Unmarshal([]byte(k.ToolCall.InputJson), &in) != nil {
			return e
		}
		b, _ := json.Marshal(cutStrings(in))
		c := proto.Clone(e).(*bandav1.Event)
		c.GetToolCall().InputJson = string(b)
		c.Truncated = true
		return c
	}
	return e
}

// cut keeps the first cutAt bytes, dropping a rune split at the end.
func cut(s string) string {
	if len(s) <= cutAt {
		return s
	}
	return strings.ToValidUTF8(s[:cutAt], "")
}

// cutStrings cuts every string in a decoded JSON value, e.g. a Write's content or an Edit's old_string.
func cutStrings(v any) any {
	switch v := v.(type) {
	case string:
		return cut(v)
	case []any:
		for i := range v {
			v[i] = cutStrings(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = cutStrings(v[k])
		}
	}
	return v
}

func (s *Service) History(ctx context.Context, req *bandav1.HistoryRequest) (*bandav1.HistoryResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	events, err := s.store.Before(ctx, req.ThreadId, req.BeforeSeq, limit)
	if err != nil {
		return nil, err
	}
	if !req.Full {
		for i, e := range events {
			events[i] = slim(e)
		}
	}
	return &bandav1.HistoryResponse{Events: events}, nil
}
