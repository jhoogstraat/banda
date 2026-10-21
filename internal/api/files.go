package api

import (
	"context"
	"crypto/rand"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// Everyone can read every thread, so everyone can read its files. The runner
// redacts the content; it passes through here unlogged.

func (s *Service) ListFiles(ctx context.Context, req *bandav1.ListFilesRequest) (*bandav1.ListFilesResponse, error) {
	res, err := s.fileRequest(ctx, req.ThreadId, &bandav1.FileRequest{List: true})
	if err != nil {
		return nil, err
	}
	return &bandav1.ListFilesResponse{Paths: res.Paths, Truncated: res.Truncated}, nil
}

func (s *Service) ReadFile(ctx context.Context, req *bandav1.ReadFileRequest) (*bandav1.ReadFileResponse, error) {
	res, err := s.fileRequest(ctx, req.ThreadId, &bandav1.FileRequest{Path: req.Path})
	if err != nil {
		return nil, err
	}
	return &bandav1.ReadFileResponse{Content: res.Content, Binary: res.Binary, Truncated: res.Truncated}, nil
}

// fileRequest asks the thread's runner and waits for its answer. It does not
// wake a paused environment.
func (s *Service) fileRequest(ctx context.Context, id string, req *bandav1.FileRequest) (*bandav1.FileResponse, error) {
	if _, err := s.store.Get(ctx, id); err != nil { // also refuses a loginKey
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req.RequestId = rand.Text()
	ch := make(chan *bandav1.FileResponse, 1)
	s.mu.Lock()
	s.files[req.RequestId] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.files, req.RequestId)
		s.mu.Unlock()
	}()
	if !s.sendRunner(id, &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_FileRequest{FileRequest: req}}) {
		return nil, errNotRunning
	}
	select {
	case res := <-ch:
		switch res.Error {
		case "":
			return res, nil
		case "not found":
			return nil, connect.NewError(connect.CodeNotFound, "file not found")
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, res.Error)
	case <-ctx.Done():
		return nil, connect.NewError(connect.CodeDeadlineExceeded, "the environment did not answer")
	}
}

// fileResponse hands a runner's answer to the waiting request, if any.
func (s *Service) fileResponse(res *bandav1.FileResponse) {
	s.mu.Lock()
	ch := s.files[res.RequestId]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default: // answered twice
		}
	}
}
