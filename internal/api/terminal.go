package api

import (
	"context"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
)

func (s *Service) OpenTerminal(context.Context, *bandav1.OpenTerminalRequest, bandav1connect.ThreadServiceOpenTerminalServerStream) error {
	return connect.NewError(connect.CodeUnimplemented, "terminal is not implemented yet")
}

func (s *Service) WriteTerminal(context.Context, *bandav1.WriteTerminalRequest) (*bandav1.WriteTerminalResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "terminal is not implemented yet")
}

func (s *Service) ResizeTerminal(context.Context, *bandav1.ResizeTerminalRequest) (*bandav1.ResizeTerminalResponse, error) {
	return nil, connect.NewError(connect.CodeUnimplemented, "terminal is not implemented yet")
}
