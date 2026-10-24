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
	// IDTokenDir holds a thread's workload identity tokens, in memory only.
	IDTokenDir = "/run/banda/id-tokens"
)

type Config struct {
	Namespace string
	Image     string
	APIURL    string // where runners dial the API
	Sizes     Sizes
}

// Sizes are what one environment may take: the limits of a thread's and of a
// login environment's pod, and the size of a thread's home volume and of each
// user's Claude Code config volume when they are created.
type Sizes struct {
	CPU, Memory, Scratch, Home          resource.Quantity
	LoginCPU, LoginMemory, LoginScratch resource.Quantity
	ClaudeVolume                        resource.Quantity
}

// ParseSizes reads the sizes from getenv (SANDBOX_CPU and the rest), with
// defaults. A limit must cover the pod's request (see limits).
func ParseSizes(getenv func(string) string) (Sizes, error) {
	var s Sizes
	for _, f := range []struct {
		key, def, min string
		q             *resource.Quantity
	}{
		{"SANDBOX_CPU", "4", "50m", &s.CPU},
		{"SANDBOX_MEMORY", "8Gi", "128Mi", &s.Memory},
		{"SANDBOX_SCRATCH", "10Gi", "128Mi", &s.Scratch},
		{"SANDBOX_HOME_SIZE", "10Gi", "1Mi", &s.Home},
		{"LOGIN_CPU", "1", "50m", &s.LoginCPU},
		{"LOGIN_MEMORY", "1Gi", "128Mi", &s.LoginMemory},
		{"LOGIN_SCRATCH", "1Gi", "128Mi", &s.LoginScratch},
		{"CLAUDE_VOLUME_SIZE", "1Gi", "1Mi", &s.ClaudeVolume},
	} {
		v := getenv(f.key)
		if v == "" {
			v = f.def
		}
		q, err := resource.ParseQuantity(v)
		if err != nil {
			return s, fmt.Errorf("%s: %q is not a Kubernetes quantity such as %s", f.key, v, f.def)
		}
		if q.Cmp(resource.MustParse(f.min)) < 0 {
			return s, fmt.Errorf("%s: %s is below the minimum of %s", f.key, v, f.min)
		}
		*f.q = q
	}
	return s, nil
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
	// client-go's default of 5 requests a second (burst 10) held a thread's start, four requests, for up
	// to a second once a few threads started together. The API server's own fairness limits stay.
	rc.QPS, rc.Burst = 50, 100
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
// the names of the team's and the owner's secrets, which the runner redacts;
// git is who the owner's commits are by.
func (c *Client) Ensure(ctx context.Context, t store.Thread, token, authMethod string, secrets []string, git store.GitIdentity) error {
	claim, err := c.ensureClaudeVolume(ctx, t.OwnerID)
	if err == nil {
		err = c.putToken(ctx, "t-"+t.ID, token)
	}
	if err != nil {
		return err
	}
	return c.start(ctx, c.sandbox("t-"+t.ID, claim, t, authMethod, secrets, git))
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
// pod reads; neither the Sandbox nor the database holds it. start makes the
// Sandbox own it, so it goes with the Sandbox.
func (c *Client) putToken(ctx context.Context, sandbox, token string) error {
	return c.patchSecret(ctx, sandbox+"-token", "runner-token", map[string][]byte{"BANDA_RUNNER_TOKEN": []byte(token)})
}

func tokenEnv(sandbox string) corev1.EnvVar {
	return corev1.EnvVar{Name: "BANDA_RUNNER_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: sandbox + "-token"}, Key: "BANDA_RUNNER_TOKEN"}}}
}

// limits bound what one sandbox takes from the node its neighbours share.
// Requests stay small: most environments idle between turns. ParseSizes
// keeps the limits at or above them.
// ponytail: one size for every thread; pids need the kubelet's podPidsLimit (deploy/kind.yaml).
func limits(cpu, memory, disk resource.Quantity) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("128Mi")},
		Limits: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: memory, corev1.ResourceEphemeralStorage: disk},
	}
}

// start creates the Sandbox if needed; otherwise it resumes it with the
// current pod template (settings may have changed) and deletes the pod so
// the controller recreates it from that, on the same volumes. A scheduled
// deletion is cancelled: the environment is in use again.
//
// Only the pod from before the patch is deleted. A suspended Sandbox has
// none, and the one the controller creates on the patch already runs the new
// template: deleting it could kill a runner that just took the thread's
// prompt, losing the turn.
func (c *Client) start(ctx context.Context, sb *sbv1.Sandbox) error {
	sandboxes := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace)
	created, err := sandboxes.Create(ctx, sb, metav1.CreateOptions{})
	if err == nil {
		return c.ownToken(ctx, created)
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	pods := c.kube.CoreV1().Pods(c.cfg.Namespace)
	old, err := pods.Get(ctx, sb.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		old = nil
	} else if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"operatingMode": "Running", "podTemplate": sb.Spec.PodTemplate,
		"shutdownTime": nil, "shutdownPolicy": nil}})
	if err != nil {
		return err
	}
	patched, err := sandboxes.Patch(ctx, sb.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err == nil {
		err = c.ownToken(ctx, patched) // Secrets from before ownership was set
	}
	if err != nil || old == nil || old.DeletionTimestamp != nil { // a pod on its way out is replaced from the new template
		return err
	}
	err = pods.Delete(ctx, sb.Name, metav1.DeleteOptions{Preconditions: metav1.NewUIDPreconditions(string(old.UID))})
	if apierrors.IsNotFound(err) || apierrors.IsConflict(err) { // gone, or already replaced
		return nil
	}
	return err
}

// ownToken makes the Sandbox the owner of its token Secret, so Kubernetes
// deletes the Secret with the Sandbox. The Secret comes first: the pod reads it.
func (c *Client) ownToken(ctx context.Context, sb *sbv1.Sandbox) error {
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"ownerReferences": []metav1.OwnerReference{{
		APIVersion: sbv1.GroupVersion.String(), Kind: "Sandbox", Name: sb.Name, UID: sb.UID}}}})
	if err != nil {
		return err
	}
	_, err = c.kube.CoreV1().Secrets(c.cfg.Namespace).Patch(ctx, sb.Name+"-token", types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// SweepTokens deletes the runner-token Secrets left behind by Sandboxes
// deleted before start made each Sandbox own its Secret: no owner, no
// Sandbox of that name, and unwritten for 10 minutes. The age spares a
// Secret whose Sandbox another API (the e2e's run side by side) is creating
// right now, between putToken and ownToken; the resourceVersion one written
// since the list.
func (c *Client) SweepTokens(ctx context.Context) (int, error) {
	secrets := c.kube.CoreV1().Secrets(c.cfg.Namespace)
	list, err := secrets.List(ctx, metav1.ListOptions{LabelSelector: "banda.dev/runner-token=true"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sec := range list.Items {
		name, ok := strings.CutSuffix(sec.Name, "-token")
		if !ok || len(sec.OwnerReferences) > 0 || time.Since(lastWrite(sec.ObjectMeta)) < 10*time.Minute {
			continue
		}
		_, err := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			if err != nil {
				return n, err
			}
			continue // start owns it on the next resume
		}
		err = secrets.Delete(ctx, sec.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &sec.ResourceVersion}})
		if err == nil {
			n++
		} else if !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return n, err
		}
	}
	return n, nil
}

// lastWrite is when an object was created or last changed by anyone.
func lastWrite(m metav1.ObjectMeta) time.Time {
	t := m.CreationTimestamp.Time
	for _, f := range m.ManagedFields {
		if f.Time != nil && f.Time.After(t) {
			t = f.Time.Time
		}
	}
	return t
}

// Count is how many Sandboxes, threads' and login environments', are
// running and suspended.
func (c *Client) Count(ctx context.Context) (running, suspended int, err error) {
	list, err := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "banda.dev/sandbox"})
	if err != nil {
		return 0, 0, err
	}
	for _, sb := range list.Items {
		if sb.Spec.OperatingMode == sbv1.SandboxOperatingModeSuspended {
			suspended++
		} else {
			running++
		}
	}
	return running, suspended, nil
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
// top of the chart's policies.yaml, with a NetworkPolicy of its own;
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

// Env is the value the owner's thread pods get for the environment variable
// name, with their precedence: banda-git < the team's < the owner's. Empty if
// none sets it. Needs get on Secrets.
func (c *Client) Env(ctx context.Context, owner, name string) (string, error) {
	var value string
	for _, s := range []string{"banda-git", teamSecret, envSecret(owner)} {
		sec, err := c.kube.CoreV1().Secrets(c.cfg.Namespace).Get(ctx, s, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		} else if err != nil {
			return "", err
		}
		if v, ok := sec.Data[name]; ok {
			value = string(v)
		}
	}
	return value, nil
}

// ForgeSecret names the Secret with a user's tokens for a forge host. Like
// the GitHub App's (banda-github-app), no pod mounts it: only the API reads
// it, and the names differ from banda-env-*, which pods load.
func ForgeSecret(owner, host string) string { return "banda-forge-" + hash(owner+" "+host) }

// Secret returns a Secret's data, nil if there is no such Secret.
func (c *Client) Secret(ctx context.Context, name string) (map[string][]byte, error) {
	sec, err := c.kube.CoreV1().Secrets(c.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sec.Data, nil
}

// PutSecret merges data into the Secret name (see patchSecret).
func (c *Client) PutSecret(ctx context.Context, name, label string, data map[string][]byte) error {
	return c.patchSecret(ctx, name, label, data)
}

// DeleteSecret deletes a Secret; one already gone is fine.
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	err := c.kube.CoreV1().Secrets(c.cfg.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
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

// ensureClaudeVolume creates the owner's Claude Code config volume. It is
// ReadWriteOnce, which every cluster has, and mounted by all of the owner's
// pods at once, so sameNode keeps those on one node.
func (c *Client) ensureClaudeVolume(ctx context.Context, owner string) (string, error) {
	name := "claude-" + hash(owner)
	_, err := c.kube.CoreV1().PersistentVolumeClaims(c.cfg.Namespace).Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"banda.dev/claude-config": "true"}},
		Spec:       pvcSpec(c.cfg.Sizes.ClaudeVolume),
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return name, nil
}

func pvcSpec(size resource.Quantity) corev1.PersistentVolumeClaimSpec {
	return corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: size},
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

func (c *Client) sandbox(name, claudeClaim string, t store.Thread, authMethod string, secrets []string, git store.GitIdentity) *sbv1.Sandbox {
	gitName, gitEmail := git.Commits()
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
		"BANDA_GIT_NAME":               gitName, // empty: the runner's default
		"BANDA_GIT_EMAIL":              gitEmail,
		"BANDA_ID_TOKEN_DIR":           IDTokenDir,
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
	labels := map[string]string{"banda.dev/thread": t.ID, "banda.dev/sandbox": "thread", // see deploy/helm/banda/templates/policies.yaml
		userLabel: hash(t.OwnerID)}
	no := false // runners never call the Kubernetes API

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
						Affinity:                      sameNode(t.OwnerID),
						Volumes:                       []corev1.Volume{claudeVolume(claudeClaim), idTokenVolume},
						Containers: []corev1.Container{{
							Name:            "runner",
							Image:           c.cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             vars,
							EnvFrom:         envFrom,
							Resources:       limits(c.cfg.Sizes.CPU, c.cfg.Sizes.Memory, c.cfg.Sizes.Scratch),
							VolumeMounts: []corev1.VolumeMount{{Name: "home", MountPath: home}, {Name: "claude", MountPath: claudeDir},
								{Name: "id-tokens", MountPath: IDTokenDir}},
						}},
					},
				},
				VolumeClaimTemplates: []sbv1.PersistentVolumeClaimTemplate{{
					EmbeddedObjectMetadata: sbv1.EmbeddedObjectMetadata{Name: "home"},
					Spec:                   pvcSpec(c.cfg.Sizes.Home),
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
	labels := map[string]string{"banda.dev/login": hash(owner), "banda.dev/sandbox": "login", userLabel: hash(owner)}
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
						Affinity:                      sameNode(owner),
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
							Resources:    limits(c.cfg.Sizes.LoginCPU, c.cfg.Sizes.LoginMemory, c.cfg.Sizes.LoginScratch),
							VolumeMounts: []corev1.VolumeMount{{Name: "home", MountPath: home}, {Name: "claude", MountPath: claudeDir}},
						}},
					},
				},
			},
			OperatingMode: sbv1.SandboxOperatingModeRunning,
		},
	}
}

// idTokenVolume keeps identity tokens in memory: never on the thread's
// volume, gone with the pod.
var idTokenVolume = corev1.Volume{Name: "id-tokens", VolumeSource: corev1.VolumeSource{
	EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: new(resource.MustParse("1Mi"))}}}

// identitySecret holds the workload identity signing keys (internal/identity):
// only the API reads it, no pod mounts it.
const identitySecret = "banda-identity"

// IdentityKeys returns the signing keys, creating the Secret with first's
// if there is none yet. Not through Helm, whose template would replace them.
func (c *Client) IdentityKeys(ctx context.Context, first func() (map[string][]byte, error)) (map[string][]byte, error) {
	secrets := c.kube.CoreV1().Secrets(c.cfg.Namespace)
	sec, err := secrets.Get(ctx, identitySecret, metav1.GetOptions{})
	if err == nil {
		return sec.Data, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	data, err := first()
	if err != nil {
		return nil, err
	}
	_, err = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: identitySecret,
		Labels: map[string]string{"banda.dev/identity": "true"}}, Data: data}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) { // another API created it first
		return c.IdentityKeys(ctx, first)
	}
	return data, err
}

// userLabel marks each pod with its owner's hash, for sameNode.
const userLabel = "banda.dev/user"

// sameNode schedules a pod onto the node where the owner's other pods run,
// so they can all mount the owner's ReadWriteOnce Claude volume. The first
// of them goes anywhere: Kubernetes lets a pod that matches its own term
// start the group. Node-local storage (local-path) pins the volume to its
// node anyway; network storage (EBS, Ceph RBD, Longhorn) would otherwise
// fail to attach it on a second node.
// Node-local storage can't follow a handoff to someone on another node
// (docs/deployment.md "Volumes").
// ponytail: one user's running pods are bounded by one node, and after a drain the scheduler
// retries them only every 5 min; an RWX Claude volume setting lifts both, add it when a team needs it.
func sameNode(owner string) *corev1.Affinity {
	return &corev1.Affinity{PodAffinity: &corev1.PodAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{userLabel: hash(owner)}},
			TopologyKey:   "kubernetes.io/hostname",
		}},
	}}
}

func claudeVolume(claim string) corev1.Volume {
	return corev1.Volume{Name: "claude", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}
}

// Waiting says what a starting thread environment's pod waits on, in
// Banda's words with Kubernetes' message, and whether it can't start by
// itself (failed). Empty when Kubernetes reports nothing in the way: no pod
// yet, pulling the image, creating the container, the runner connecting.
func (c *Client) Waiting(ctx context.Context, threadID string) (why string, failed bool, err error) {
	return c.waiting(ctx, "t-"+threadID)
}

func (c *Client) WaitingLogin(ctx context.Context, owner string) (string, bool, error) {
	return c.waiting(ctx, "login-"+hash(owner))
}

func (c *Client) waiting(ctx context.Context, name string) (string, bool, error) {
	pod, err := c.kube.CoreV1().Pods(c.cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	if pod.DeletionTimestamp != nil { // the one before, on its way out
		return "", false, nil
	}
	why, failed := podWaiting(pod)
	return why, failed, nil
}

// podWaiting reads a pod's status. Only what can't change without someone
// acting fails: an image Kubernetes already failed to pull and backs off
// from, a container it can't configure (a missing Secret), a runner that
// keeps exiting. Retry recreates the pod, which tries again. The rest is
// shown and waited for, however long: the scheduler finds room once a node
// frees up, and a big image takes minutes to pull.
func podWaiting(p *corev1.Pod) (string, bool) {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			why := "Waiting for room on a node: " + c.Message
			if strings.Contains(c.Message, "volume node affinity") || strings.Contains(c.Message, "PersistentVolume's node affinity") {
				// Node-local storage: the home volume is on another node than its owner's other pods (sameNode).
				why += " Its volume is on another node than its owner's other environments: hand the thread back or fork it."
			}
			return why, false
		}
	}
	for _, s := range p.Status.ContainerStatuses {
		w := s.State.Waiting
		if w == nil {
			continue
		}
		switch w.Reason {
		case "ErrImagePull":
			return "Can't pull the sandbox image, retrying: " + w.Message, false
		case "ImagePullBackOff", "InvalidImageName":
			return "Can't pull the sandbox image: " + w.Message, true
		case "CreateContainerConfigError":
			return "Can't configure the sandbox's container: " + w.Message, true
		case "CrashLoopBackOff":
			why := "The runner keeps exiting before it connects"
			if t := s.LastTerminationState.Terminated; t != nil {
				why += fmt.Sprintf(" (exit code %d, %s)", t.ExitCode, t.Reason)
			}
			return why, true
		}
		if w.Message != "" {
			return w.Reason + ": " + w.Message, false
		}
	}
	return "", false
}
