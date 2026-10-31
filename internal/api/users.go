package api

import (
	"context"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func (s *Service) Me(ctx context.Context, _ *bandav1.MeRequest) (*bandav1.MeResponse, error) {
	id := user(ctx)
	if err := s.store.EnsureUser(ctx, id); err != nil {
		return nil, err
	}
	users, err := s.store.Users(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.ID == id {
			return &bandav1.MeResponse{User: &bandav1.User{Id: u.ID, Name: u.Name, Email: u.Email}, TeamIdentity: s.TeamIdentity}, nil
		}
	}
	return &bandav1.MeResponse{User: &bandav1.User{Id: id, Name: id}}, nil
}

func (s *Service) ListUsers(ctx context.Context, _ *bandav1.ListUsersRequest) (*bandav1.ListUsersResponse, error) {
	users, err := s.store.Users(ctx)
	if err != nil {
		return nil, err
	}
	res := &bandav1.ListUsersResponse{}
	for _, u := range users {
		res.Users = append(res.Users, &bandav1.User{Id: u.ID, Name: u.Name, Email: u.Email})
	}
	return res, nil
}

// names maps user ids to display names.
func (s *Service) names(ctx context.Context) map[string]string {
	users, _ := s.store.Users(ctx)
	m := map[string]string{}
	for _, u := range users {
		m[u.ID] = u.Name
	}
	return m
}
