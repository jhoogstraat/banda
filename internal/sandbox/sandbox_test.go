package sandbox

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
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

func TestParseCIDRs(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "",
		" , ":                           "",
		"10.20.0.0/16":                  "10.20.0.0/16",
		" 10.20.0.0/16 , 192.0.2.7/32,": "10.20.0.0/16 192.0.2.7/32",
		"fd00::/8":                      "fd00::/8",
	} {
		got, err := ParseCIDRs(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		var s []string
		for _, p := range got {
			s = append(s, p.String())
		}
		if strings.Join(s, " ") != want {
			t.Errorf("%q = %v, want %s", in, s, want)
		}
	}
	for in, want := range map[string]string{
		"10.20.0.0":              `"10.20.0.0" is not a CIDR`,
		"db.internal/32":         `"db.internal/32" is not a CIDR`,
		"10.20.0.0/33":           `"10.20.0.0/33" is not a CIDR`,
		"10.0.0.0/8;10.1.0.0/16": `"10.0.0.0/8;10.1.0.0/16" is not a CIDR`,
		"10.20.30.40/16":         `"10.20.30.40/16" has host bits set: did you mean 10.20.0.0/16?`,
	} {
		if _, err := ParseCIDRs(in); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: error %v, want %s", in, err, want)
		}
	}
}

// The policy adds the ranges to deploy/sandbox-policy.yaml for every sandbox
// pod, egress only: policies add up, so nothing there is loosened.
func TestEgressPolicy(t *testing.T) {
	b, err := json.Marshal(egressPolicy([]netip.Prefix{netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("192.0.2.7/32")}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"NetworkPolicy","apiVersion":"networking.k8s.io/v1","metadata":{"name":"sandboxes-allow"},"spec":{` +
		`"podSelector":{"matchExpressions":[{"key":"banda.dev/sandbox","operator":"Exists"}]},` +
		`"egress":[{"to":[{"ipBlock":{"cidr":"10.20.0.0/16"}},{"ipBlock":{"cidr":"192.0.2.7/32"}}]}],"policyTypes":["Egress"]}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}
