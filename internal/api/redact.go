package api

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// handedOut remembers, per thread, the git credentials GitCredential gave
// its runner until they expire, so whatever the runner sends back is stored
// and broadcast without them (redactRunner). The runner redacts known token
// shapes itself, but GitLab's OAuth tokens have none, and the pod is
// untrusted: the API redacts what it knows it handed out.
type handedOut struct {
	mu sync.Mutex
	m  map[string][]credential // thread
}

type credential struct {
	value   string
	expires time.Time
}

// credentialTTL bounds how long a credential without an expiry is remembered.
const credentialTTL = 2 * time.Hour

// remember notes a credential handed to thread's runner.
func (h *handedOut) remember(thread, value string, expires time.Time) {
	if len(value) < 8 { // shorter would redact ordinary text
		return
	}
	if expires.IsZero() {
		expires = time.Now().Add(credentialTTL)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = map[string][]credential{}
	}
	cs := h.m[thread][:0:0]
	for _, c := range h.m[thread] {
		if c.value != value && time.Now().Before(c.expires) {
			cs = append(cs, c)
		}
	}
	h.m[thread] = append(cs, credential{value, expires})
}

// live is what thread's runner holds now: current and previous after a rotation.
func (h *handedOut) live(thread string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.m[thread] {
		if time.Now().Before(c.expires) {
			out = append(out, c.value)
		}
	}
	if len(out) == 0 {
		delete(h.m, thread)
	}
	return out
}

// redactRunner replaces the credentials handed to thread's runner with
// [redacted] in every string and bytes field of msg: events (tool output,
// agent text), terminal output, diffs, files. Preview traffic passes as it
// is: it isn't stored, and changing its bytes would break the app.
// ponytail: a token split across two terminal chunks slips through; buffer a token's length if that matters.
func (s *Service) redactRunner(thread string, msg *bandav1.ConnectRequest) {
	if msg.GetHttpTunnel() != nil {
		return
	}
	if values := s.handedOut.live(thread); len(values) > 0 {
		scrubMessage(msg.ProtoReflect(), values)
	}
}

func scrubMessage(m protoreflect.Message, values []string) {
	var later []func() // a message isn't changed while it is ranged over
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList():
			l := v.List()
			for i := range l.Len() {
				if x, ok := scrubValue(fd, l.Get(i), values); ok {
					l.Set(i, x)
				}
			}
		case fd.IsMap():
			mp := v.Map()
			mp.Range(func(k protoreflect.MapKey, x protoreflect.Value) bool {
				if y, ok := scrubValue(fd.MapValue(), x, values); ok {
					later = append(later, func() { mp.Set(k, y) })
				}
				return true
			})
		default:
			if x, ok := scrubValue(fd, v, values); ok {
				later = append(later, func() { m.Set(fd, x) })
			}
		}
		return true
	})
	for _, f := range later {
		f()
	}
}

// scrubValue is v without values, and whether it changed (messages change in place).
func scrubValue(fd protoreflect.FieldDescriptor, v protoreflect.Value, values []string) (protoreflect.Value, bool) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		s, changed := v.String(), false
		for _, x := range values {
			if strings.Contains(s, x) {
				s, changed = strings.ReplaceAll(s, x, "[redacted]"), true
			}
		}
		return protoreflect.ValueOfString(s), changed
	case protoreflect.BytesKind:
		b, changed := v.Bytes(), false
		for _, x := range values {
			if bytes.Contains(b, []byte(x)) {
				b, changed = bytes.ReplaceAll(b, []byte(x), []byte("[redacted]")), true
			}
		}
		return protoreflect.ValueOfBytes(b), changed
	case protoreflect.MessageKind, protoreflect.GroupKind:
		scrubMessage(v.Message(), values)
	}
	return v, false
}
