// Package sandbox creates one agent-sandbox Sandbox per thread. The home
// directory is a per-thread PVC (the checkout survives pod restarts and
// suspends). Claude Code's config dir is a per-user PVC that only the claude
// binary touches, so a user logs in once for all their threads. Each user
// also has a small login environment and two Secrets: their own environment
// variables and, in API key mode, the Anthropic API key. The team's
// environment variables are one more Secret, in every thread.
// See docs/research/claude-login.md and docs/research/sandbox-lifecycle.md.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	sbv1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"

	"github.com/jhoogstraat/banda/internal/store"
)

const (
	home      = "/home/banda"
	claudeDir = home + "/.claude"
)

type Config struct {
	Namespace string
	Image     string
	APIURL    string // where runners dial the API
}

type Client struct {
	cfg  Config
	sbx  versioned.Interface
	kube kubernetes.Interface
}

// New uses the kubeconfig, or the in-cluster config when there is none.
func New(cfg Config) (*Client, error) {
	rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	sbx, err := versioned.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	return &Client{cfg, sbx, kube}, nil
}

// Ensure gives the thread a freshly started pod, with token as its runner
// token. authMethod is the owner's: "subscription" or "api_key"; secrets are
// the names of the team's and the owner's secrets, which the runner redacts.
func (c *Client) Ensure(ctx context.Context, t store.Thread, token, authMethod string, secrets []string) error {
	claim, err := c.ensureClaudeVolume(ctx, t.OwnerID)
	if err == nil {
		err = c.putToken(ctx, "t-"+t.ID, token)
	}
	if err != nil {
		return err
	}
	return c.start(ctx, c.sandbox("t-"+t.ID, claim, t, authMethod, secrets))
}

// EnsureLogin gives the user's login environment a freshly started pod, with
// a runner token from newToken. A pod that is still starting is kept, so
// opening the terminal twice doesn't restart it.
func (c *Client) EnsureLogin(ctx context.Context, owner string, newToken func() (string, error)) error {
	claim, err := c.ensureClaudeVolume(ctx, owner)
	if err != nil {
		return err
	}
	name := "login-" + hash(owner)
	pod, err := c.kube.CoreV1().Pods(c.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil && pod.DeletionTimestamp == nil && time.Since(pod.CreationTimestamp.Time) < time.Minute {
		return nil
	}
	token, err := newToken()
	if err == nil {
		err = c.putToken(ctx, name, token)
	}
	if err != nil {
		return err
	}
	return c.start(ctx, c.loginSandbox(owner, claim))
}

// putToken keeps a sandbox's runner token in a Secret of its own, which its
// pod reads; neither the Sandbox nor the database holds it.
// ponytail: the Secret outlives a deleted Sandbox (one per thread, holding a token that opens nothing without its pod).
func (c *Client) putToken(ctx context.Context, sandbox, token string) error {
	return c.patchSecret(ctx, sandbox+"-token", "runner-token", map[string][]byte{"BANDA_RUNNER_TOKEN": []byte(token)})
}

func tokenEnv(sandbox string) corev1.EnvVar {
	return corev1.EnvVar{Name: "BANDA_RUNNER_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: sandbox + "-token"}, Key: "BANDA_RUNNER_TOKEN"}}}
}

// limits bound what one sandbox takes from the node its neighbours share.
// Requests stay small: most environments idle between turns.
// ponytail: fixed sizes, make them settings when a team needs bigger builds; pids need the kubelet's podPidsLimit (deploy/kind.yaml).
func limits(cpu, memory, disk string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("128Mi")},
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory),
			corev1.ResourceEphemeralStorage: resource.MustParse(disk)},
	}
}

// start creates the Sandbox if needed; otherwise it resumes it with the
// current pod template (settings may have changed) and deletes the pod so
// the controller recreates it from that, on the same volumes. A scheduled
// deletion is cancelled: the environment is in use again.
func (c *Client) start(ctx context.Context, sb *sbv1.Sandbox) error {
	sandboxes := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace)
	_, err := sandboxes.Create(ctx, sb, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"operatingMode": "Running", "podTemplate": sb.Spec.PodTemplate,
		"shutdownTime": nil, "shutdownPolicy": nil}})
	if err != nil {
		return err
	}
	if _, err := sandboxes.Patch(ctx, sb.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return err
	}
	err = c.kube.CoreV1().Pods(c.cfg.Namespace).Delete(ctx, sb.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// ParseCIDRs parses a comma-separated list of CIDRs (SANDBOX_EGRESS_ALLOW).
// NetworkPolicies can't name hosts, so a host is an address with /32.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var cidrs []netip.Prefix
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR such as 10.20.0.0/16, or 10.20.30.40/32 for one address", f)
		}
		if p != p.Masked() {
			return nil, fmt.Errorf("%q has host bits set: did you mean %s?", f, p.Masked())
		}
		cidrs = append(cidrs, p)
	}
	return cidrs, nil
}

// AllowEgress lets the namespace's sandboxes reach cidrs (production, say) on
// top of deploy/sandbox-policy.yaml, with a NetworkPolicy of its own;
// policies add up, so the base stays as it is. None removes it. Banda's own
// pods keep sandboxes out with ingress policies even when a range covers them.
func (c *Client) AllowEgress(ctx context.Context, cidrs []netip.Prefix) error {
	policies := c.kube.NetworkingV1().NetworkPolicies(c.cfg.Namespace)
	if len(cidrs) == 0 { // never apply it empty: a rule without destinations allows them all
		err := policies.Delete(ctx, egressPolicyName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	body, err := json.Marshal(egressPolicy(cidrs))
	if err != nil {
		return err
	}
	force := true
	_, err = policies.Patch(ctx, egressPolicyName, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: "banda-api", Force: &force})
	return err
}

const egressPolicyName = "sandboxes-allow"

func egressPolicy(cidrs []netip.Prefix) *networkingv1.NetworkPolicy {
	var to []networkingv1.NetworkPolicyPeer
	for _, c := range cidrs {
		to = append(to, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: c.String()}})
	}
	return &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: egressPolicyName},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "banda.dev/sandbox", Operator: metav1.LabelSelectorOpExists}}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      []networkingv1.NetworkPolicyEgressRule{{To: to}},
		},
	}
}

func hash(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:8])
}

func envSecret(owner string) string  { return "banda-env-" + hash(owner) }
func authSecret(owner string) string { return "banda-auth-" + hash(owner) }

const teamSecret = "banda-env-team" // hashes are hex, so no user's

// SetSecret sets the user's environment variable; a nil value removes it.
func (c *Client) SetSecret(ctx context.Context, owner, name string, value []byte) error {
	return c.patchSecret(ctx, envSecret(owner), "user-secret", map[string][]byte{name: value})
}

// SetTeamSecret sets an environment variable of every thread; a nil value removes it.
func (c *Client) SetTeamSecret(ctx context.Context, name string, value []byte) error {
	return c.patchSecret(ctx, teamSecret, "team-secret", map[string][]byte{name: value})
}

func (c *Client) SetAPIKey(ctx context.Context, owner, key string) error {
	return c.patchSecret(ctx, authSecret(owner), "user-secret", map[string][]byte{"ANTHROPIC_API_KEY": []byte(key)})
}

// patchSecret merges data into a Secret, creating it if needed with the
// label banda.dev/<label>. Nil values remove keys.
func (c *Client) patchSecret(ctx context.Context, name, label string, data map[string][]byte) error {
	secrets := c.kube.CoreV1().Secrets(c.cfg.Namespace)
	patch, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return err
	}
	_, err = secrets.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if !apierrors.IsNotFound(err) {
		return err
	}
	maps.DeleteFunc(data, func(_ string, v []byte) bool { return v == nil })
	if len(data) == 0 {
		return nil
	}
	_, err = secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"banda.dev/" + label: "true"}},
		Data:       data,
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) { // a concurrent first write won
		_, err = secrets.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

// ensureClaudeVolume creates the owner's Claude Code config volume.
// ponytail: RWO works while all pods run on one node (kind); multi-node needs RWX or node pinning.
func (c *Client) ensureClaudeVolume(ctx context.Context, owner string) (string, error) {
	name := "claude-" + hash(owner)
	_, err := c.kube.CoreV1().PersistentVolumeClaims(c.cfg.Namespace).Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"banda.dev/claude-config": "true"}},
		Spec:       pvcSpec("1Gi"),
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return name, nil
}

func pvcSpec(size string) corev1.PersistentVolumeClaimSpec {
	return corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
		},
	}
}

// Suspend deletes the thread's pod and keeps its volumes.
func (c *Client) Suspend(ctx context.Context, threadID string) error {
	return c.suspend(ctx, "t-"+threadID)
}

func (c *Client) SuspendLogin(ctx context.Context, owner string) error {
	return c.suspend(ctx, "login-"+hash(owner))
}

// ScheduleDelete makes the controller delete the thread's Sandbox, and with
// it the home volume, at the given time. Nil cancels that. A Sandbox that is
// already gone is fine.
func (c *Client) ScheduleDelete(ctx context.Context, threadID string, at *time.Time) error {
	spec := map[string]any{"shutdownTime": nil, "shutdownPolicy": nil}
	if at != nil {
		spec = map[string]any{"shutdownTime": at.UTC().Format(time.RFC3339), "shutdownPolicy": sbv1.ShutdownPolicyDelete}
	}
	patch, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return err
	}
	_, err = c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace).Patch(ctx, "t-"+threadID, types.MergePatchType, patch, metav1.PatchOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) suspend(ctx context.Context, name string) error {
	_, err := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace).Patch(ctx, name, types.MergePatchType,
		[]byte(`{"spec":{"operatingMode":"Suspended"}}`), metav1.PatchOptions{})
	return err
}

func (c *Client) sandbox(name, claudeClaim string, t store.Thread, authMethod string, secrets []string) *sbv1.Sandbox {
	env := map[string]string{
		"HOME":                         home,
		"CLAUDE_CONFIG_DIR":            claudeDir,
		"CLAUDE_CODE_PROJECT_DIR_NAME": t.ID, // keeps each thread's transcripts apart in the shared dir
		"BANDA_THREAD_ID":              t.ID,
		"BANDA_API_URL":                c.cfg.APIURL,
		"BANDA_REPO_URL":               t.RepoURL,
		"BANDA_BASE_BRANCH":            t.BaseBranch,
		"BANDA_BRANCH":                 t.Branch,
		"BANDA_MODEL":                  t.Model,
		"BANDA_EFFORT":                 t.Effort,
		"BANDA_PROFILE":                t.Profile,
		"BANDA_AUTH_METHOD":            authMethod,
		"BANDA_SECRET_NAMES":           strings.Join(secrets, ","),
	}
	if t.Handoffs > 0 { // a new owner's session: the old one's transcript is on the old owner's volume
		env["BANDA_SESSION_KEY"] = fmt.Sprintf("/handoff-%d", t.Handoffs)
	}
	vars := []corev1.EnvVar{tokenEnv(name)}
	for k, v := range env {
		vars = append(vars, corev1.EnvVar{Name: k, Value: v})
	}
	optional := true
	from := func(secret string) corev1.EnvFromSource {
		return corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Optional: &optional}}
	}
	// For a key in several sources Kubernetes takes the last: the user's own
	// variables (GIT_TOKEN for private repos, say) beat the team's, which beat
	// the admin-made "banda-git".
	envFrom := []corev1.EnvFromSource{from("banda-git"), from(teamSecret), from(envSecret(t.OwnerID))}
	if authMethod == "api_key" { // overrides the subscription login, so only when chosen
		envFrom = append(envFrom, from(authSecret(t.OwnerID)))
	}
	uid := int64(1000)
	grace := int64(90)                                                                   // the runner stops claude gracefully; killing it mid token refresh logs the user out
	labels := map[string]string{"banda.dev/thread": t.ID, "banda.dev/sandbox": "thread"} // see deploy/sandbox-policy.yaml
	no := false                                                                          // runners never call the Kubernetes API

	return &sbv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: sbv1.SandboxSpec{
			SandboxBlueprint: sbv1.SandboxBlueprint{
				PodTemplate: sbv1.PodTemplate{
					ObjectMeta: sbv1.PodMetadata{Labels: labels},
					Spec: corev1.PodSpec{
						SecurityContext:               &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid},
						TerminationGracePeriodSeconds: &grace,
						AutomountServiceAccountToken:  &no,
						Volumes:                       []corev1.Volume{claudeVolume(claudeClaim)},
						Containers: []corev1.Container{{
							Name:            "runner",
							Image:           c.cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             vars,
							EnvFrom:         envFrom,
							Resources:       limits("4", "8Gi", "10Gi"),
							VolumeMounts:    []corev1.VolumeMount{{Name: "home", MountPath: home}, {Name: "claude", MountPath: claudeDir}},
						}},
					},
				},
				VolumeClaimTemplates: []sbv1.PersistentVolumeClaimTemplate{{
					EmbeddedObjectMetadata: sbv1.EmbeddedObjectMetadata{Name: "home"},
					Spec:                   pvcSpec("10Gi"),
				}},
			},
			OperatingMode: sbv1.SandboxOperatingModeRunning,
		},
	}
}

// loginSandbox runs the runner in login mode: no checkout, no claude session,
// no secrets. It mounts only the user's Claude config volume.
func (c *Client) loginSandbox(owner, claudeClaim string) *sbv1.Sandbox {
	uid := int64(1000)
	grace := int64(90)
	no := false
	name := "login-" + hash(owner)
	labels := map[string]string{"banda.dev/login": hash(owner), "banda.dev/sandbox": "login"}
	return &sbv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: sbv1.SandboxSpec{
			SandboxBlueprint: sbv1.SandboxBlueprint{
				PodTemplate: sbv1.PodTemplate{
					ObjectMeta: sbv1.PodMetadata{Labels: labels},
					Spec: corev1.PodSpec{
						SecurityContext:               &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid},
						TerminationGracePeriodSeconds: &grace,
						AutomountServiceAccountToken:  &no,
						Volumes: []corev1.Volume{claudeVolume(claudeClaim),
							{Name: "home", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
						Containers: []corev1.Container{{
							Name:            "runner",
							Image:           c.cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "HOME", Value: home},
								{Name: "CLAUDE_CONFIG_DIR", Value: claudeDir},
								{Name: "BANDA_API_URL", Value: c.cfg.APIURL},
								{Name: "BANDA_LOGIN_USER_ID", Value: owner},
								tokenEnv(name),
							},
							Resources:    limits("1", "1Gi", "1Gi"),
							VolumeMounts: []corev1.VolumeMount{{Name: "home", MountPath: home}, {Name: "claude", MountPath: claudeDir}},
						}},
					},
				},
			},
			OperatingMode: sbv1.SandboxOperatingModeRunning,
		},
	}
}

func claudeVolume(claim string) corev1.Volume {
	return corev1.Volume{Name: "claude", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}
}
