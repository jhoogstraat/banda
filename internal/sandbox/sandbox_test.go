package sandbox

import (
	"slices"
	"testing"

	"github.com/jhoogstraat/banda/internal/store"
)

// The last envFrom source wins a duplicate key, so the order is what makes a
// user's own secret beat the team's, and the team's the admin's banda-git.
func TestEnvFromOrder(t *testing.T) {
	c := &Client{}
	for method, want := range map[string][]string{
		"subscription": {"banda-git", "banda-env-team", envSecret("alice")},
		"api_key":      {"banda-git", "banda-env-team", envSecret("alice"), authSecret("alice")},
	} {
		var got []string
		for _, f := range c.sandbox("t-1", "claude", store.Thread{ID: "1", OwnerID: "alice"}, method, nil).Spec.PodTemplate.Spec.Containers[0].EnvFrom {
			got = append(got, f.SecretRef.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: envFrom %v, want %v", method, got, want)
		}
	}
}
