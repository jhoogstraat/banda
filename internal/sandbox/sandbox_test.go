package sandbox

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	sbfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

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
		for _, f := range c.sandbox("t-1", "claude", store.Thread{ID: "1", OwnerID: "alice"}, method, nil, store.GitIdentity{}).Spec.PodTemplate.Spec.Containers[0].EnvFrom {
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

func TestParseSizes(t *testing.T) {
	s, err := ParseSizes(func(k string) string { return map[string]string{"SANDBOX_MEMORY": "16Gi", "LOGIN_CPU": "500m"}[k] })
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{cfg: Config{Sizes: s}}
	sb := c.sandbox("t-1", "claude", store.Thread{ID: "1"}, "subscription", nil, store.GitIdentity{})
	lim := sb.Spec.PodTemplate.Spec.Containers[0].Resources.Limits
	if lim.Memory().String() != "16Gi" || lim.Cpu().String() != "4" || lim.StorageEphemeral().String() != "10Gi" {
		t.Errorf("thread limits %v", lim)
	}
	if got := sb.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests.Storage().String(); got != "10Gi" {
		t.Errorf("home volume %s", got)
	}
	if got := c.loginSandbox("alice", "claude").Spec.PodTemplate.Spec.Containers[0].Resources.Limits.Cpu().String(); got != "500m" {
		t.Errorf("login cpu %s", got)
	}
	for env, want := range map[string]string{
		"SANDBOX_CPU=lots":        `SANDBOX_CPU: "lots" is not a Kubernetes quantity such as 4`,
		"SANDBOX_MEMORY=8G i":     `SANDBOX_MEMORY: "8G i" is not a Kubernetes quantity`,
		"LOGIN_MEMORY=64Mi":       "LOGIN_MEMORY: 64Mi is below the minimum of 128Mi",
		"SANDBOX_HOME_SIZE=0":     "SANDBOX_HOME_SIZE: 0 is below the minimum",
		"CLAUDE_VOLUME_SIZE=-1Gi": "CLAUDE_VOLUME_SIZE: -1Gi is below the minimum",
	} {
		k, v, _ := strings.Cut(env, "=")
		if _, err := ParseSizes(func(key string) string { return map[string]string{k: v}[key] }); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: error %v, want %s", env, err, want)
		}
	}
}

// The policy adds the ranges to the chart's policies.yaml for every sandbox
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

// Resuming a suspended Sandbox must not delete the pod the controller creates
// for it: its runner may already have taken the thread's prompt. A pod from
// before is replaced.
func TestStartKeepsTheNewPod(t *testing.T) {
	for _, running := range []bool{false, true} {
		ctx := context.Background()
		sb := (&Client{}).sandbox("t-1", "claude", store.Thread{ID: "1", OwnerID: "alice"}, "subscription", nil, store.GitIdentity{})
		sb.Namespace = "ns"
		sbx := sbfake.NewSimpleClientset()
		sbx.AgentsV1beta1().Sandboxes("ns").Create(ctx, sb.DeepCopy(), metav1.CreateOptions{}) // suspended or running, it exists
		pod := func(uid string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "t-1", Namespace: "ns", UID: k8stypes.UID(uid)}}
		}
		objs := []runtime.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "t-1-token", Namespace: "ns"}}}
		if running {
			objs = append(objs, pod("old"))
		}
		kube := kubefake.NewClientset(objs...)
		// The controller: a Running Sandbox without a pod gets one.
		sbx.PrependReactor("patch", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
			kube.CoreV1().Pods("ns").Create(ctx, pod("new"), metav1.CreateOptions{})
			return false, nil, nil
		})
		c := &Client{cfg: Config{Namespace: "ns"}, sbx: sbx, kube: kube}
		if err := c.start(ctx, sb); err != nil {
			t.Fatal(err)
		}
		got, err := kube.CoreV1().Pods("ns").Get(ctx, "t-1", metav1.GetOptions{})
		if running && !apierrors.IsNotFound(err) {
			t.Errorf("the running pod was kept: %v", got.UID)
		}
		if !running && (err != nil || got.UID != "new") {
			t.Errorf("the controller's new pod was deleted: %v", err)
		}
	}
}
