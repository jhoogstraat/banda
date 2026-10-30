package api

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

const (
	maxImage = 5 << 20 // the Messages API's limit per image
	// Text files are inlined into the prompt, so they cost context.
	maxText = 256 << 10
	// Per message.
	maxAttachments = 5
	// Per prompt, which may fold in several messages: the latest ones win.
	maxPromptImages = 20
)

// The image types the Messages API takes. Anything else must be UTF-8 text.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// FilesHandler serves uploads, POST /files?name=<name> with the file as the
// body (the reply is the Attachment as JSON), and downloads, GET /files/<id>.
// user authenticates the request ("" = signed out). Everyone signed in may
// read every attachment, like every thread.
func (s *Service) FilesHandler(user func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		by := user(r)
		if by == "" {
			http.Error(w, "sign in required", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/files" {
			// A text/plain upload needs no CORS preflight: refuse one from a
			// previewed app (same site, so with the viewer's cookie).
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				http.Error(w, "uploads only from Banda", http.StatusForbidden)
				return
			}
			s.upload(w, r, by)
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, "/files/")
		if r.Method != http.MethodGet || !ok {
			http.NotFound(w, r)
			return
		}
		a, err := s.store.Attachments(r.Context(), []string{id}, true)
		if errors.Is(err, store.ErrNoAttachment) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			slog.Error("read attachment", "id", id, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Uploaded content on our origin: never let a browser run it.
		h := w.Header()
		h.Set("Content-Type", a[0].MediaType+map[bool]string{false: "; charset=utf-8"}[imageTypes[a[0].MediaType]])
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Disposition", "inline; filename="+strconv.Quote(a[0].Name))
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Write(a[0].Data)
	})
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request, by string) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImage))
	if err != nil {
		http.Error(w, "files can be at most 5 MB", http.StatusRequestEntityTooLarge)
		return
	}
	a := store.Attachment{ID: strings.ToLower(rand.Text()), UserID: by, Name: r.URL.Query().Get("name"), MediaType: r.Header.Get("Content-Type"), Data: data}
	if !imageTypes[a.MediaType] {
		if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			http.Error(w, "only PNG, JPEG, GIF and WebP images and text files can be attached", http.StatusUnsupportedMediaType)
			return
		}
		if len(data) > maxText {
			http.Error(w, "text files can be at most 256 KB", http.StatusRequestEntityTooLarge)
			return
		}
		a.MediaType = "text/plain"
	}
	if r := []rune(a.Name); len(r) > 200 {
		a.Name = string(r[:200])
	}
	a.Name = or(a.Name, "file")
	if err := s.store.CreateAttachment(r.Context(), a); err != nil {
		slog.Error("store attachment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.Size = int64(len(data))
	body, _ := protojson.Marshal(attachmentProto(a))
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func attachmentProto(a store.Attachment) *bandav1.Attachment {
	return &bandav1.Attachment{Id: a.ID, Name: a.Name, MediaType: a.MediaType, Size: a.Size}
}

// checkAttachments replaces the client's attachment list with the stored
// metadata, and refuses attachments that someone else uploaded, unless by owns
// threadID ("" for none) and a proposal there carries them (Use as draft).
func (s *Service) checkAttachments(ctx context.Context, m *bandav1.Message, by, threadID string) error {
	if len(m.Attachments) > maxAttachments {
		return connect.NewError(connect.CodeInvalidArgument, "at most 5 files per message")
	}
	if len(m.Attachments) == 0 {
		return nil
	}
	var ids []string
	for _, a := range m.Attachments {
		ids = append(ids, a.GetId())
	}
	stored, err := s.store.Attachments(ctx, ids, false)
	if errors.Is(err, store.ErrNoAttachment) {
		return connect.NewError(connect.CodeInvalidArgument, "unknown attachment")
	} else if err != nil {
		return err
	}
	var proposed map[string]bool
	m.Attachments = nil
	for _, a := range stored {
		if a.UserID != by && proposed == nil {
			if proposed, err = s.proposedFiles(ctx, threadID, by); err != nil {
				return err
			}
		}
		if a.UserID != by && !proposed[a.ID] {
			return connect.NewError(connect.CodeInvalidArgument, "unknown attachment")
		}
		m.Attachments = append(m.Attachments, attachmentProto(a))
	}
	return nil
}

// proposedFiles are the ids of the files on threadID's proposals, which only
// its owner may post again. Never nil.
func (s *Service) proposedFiles(ctx context.Context, threadID, by string) (map[string]bool, error) {
	ids := map[string]bool{}
	if threadID == "" {
		return ids, nil
	}
	t, err := s.store.Get(ctx, threadID)
	if err != nil || t.OwnerID != by {
		return ids, err
	}
	events, err := s.store.EventsOf(ctx, threadID, 0, "message")
	if err != nil {
		return ids, err
	}
	for _, m := range messages(events) {
		for _, a := range m.Attachments {
			if m.Proposal {
				ids[a.Id] = true
			}
		}
	}
	return ids, nil
}
