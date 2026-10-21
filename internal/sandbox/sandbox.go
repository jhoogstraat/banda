// Package sandbox creates one agent-sandbox Sandbox per thread. The home
// directory is a per-thread PVC (the checkout survives pod restarts and
// suspends). Claude Code's config dir is a per-user PVC that only the claude
// binary touches, so a user logs in once for all their threads. Each user
// also has a small login environment and two Secrets: their own environment
// variables and, in API key mode, the Anthropic API key.
// See docs/research/claude-login.md and docs/research/sandbox-lifecycle.md.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
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

// Ensure gives the thread a freshly started pod. authMethod is the owner's:
// "subscription" or "api_key"; secrets are the names of the owner's secrets,
// which the runner redacts.
func (c *Client) Ensure(ctx context.Context, t store.Thread, authMethod string, secrets []string) error {
	claim, err := c.ensureClaudeVolume(ctx, t.OwnerID)
	if err != nil {
		return err
	}
	return c.start(ctx, c.sandbox("t-"+t.ID, claim, t, authMethod, secrets))
}

// EnsureLogin gives the user's login environment a freshly started pod.
// A pod that is still starting is kept, so opening the terminal twice
// doesn't restart it.
func (c *Client) EnsureLogin(ctx context.Context, owner, token string) error {
	claim, err := c.ensureClaudeVolume(ctx, owner)
	if err != nil {
		return err
	}
	pod, err := c.kube.CoreV1().Pods(c.cfg.Namespace).Get(ctx, "login-"+hash(owner), metav1.GetOptions{})
	if err == nil && pod.DeletionTimestamp == nil && time.Since(pod.CreationTimestamp.Time) < time.Minute {
		return nil
	}
	return c.start(ctx, c.loginSandbox(owner, claim, token))
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

func hash(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:8])
}

func envSecret(owner string) string  { return "banda-env-" + hash(owner) }
func authSecret(owner string) string { return "banda-auth-" + hash(owner) }

// SetSecret sets the user's environment variable; a nil value removes it.
func (c *Client) SetSecret(ctx context.Context, owner, name string, value []byte) error {
	return c.patchSecret(ctx, envSecret(owner), map[string][]byte{name: value})
}

func (c *Client) SetAPIKey(ctx context.Context, owner, key string) error {
	return c.patchSecret(ctx, authSecret(owner), map[string][]byte{"ANTHROPIC_API_KEY": []byte(key)})
}

// patchSecret merges data into a Secret, creating it if needed. Nil values
// remove keys.
func (c *Client) patchSecret(ctx context.Context, name string, data map[string][]byte) error {
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
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"banda.dev/user-secret": "true"}},
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
		"BANDA_RUNNER_TOKEN":           t.RunnerToken, // ponytail: plain env on the CR, move to a per-thread Secret before sharing the cluster
		"BANDA_API_URL":                c.cfg.APIURL,
		"BANDA_REPO_URL":               t.RepoURL,
		"BANDA_BASE_BRANCH":            t.BaseBranch,
		"BANDA_BRANCH":                 t.Branch,
		"BANDA_MODEL":                  t.Model,
		"BANDA_EFFORT":                 t.Effort,
		"BANDA_AUTH_METHOD":            authMethod,
		"BANDA_SECRET_NAMES":           strings.Join(secrets, ","),
	}
	var vars []corev1.EnvVar
	for k, v := range env {
		vars = append(vars, corev1.EnvVar{Name: k, Value: v})
	}
	optional := true
	from := func(secret string) corev1.EnvFromSource {
		return corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Optional: &optional}}
	}
	// Later sources win: the user's own variables (GIT_TOKEN for private
	// repos, say) beat the shared, admin-made "banda-git".
	envFrom := []corev1.EnvFromSource{from("banda-git"), from(envSecret(t.OwnerID))}
	if authMethod == "api_key" { // overrides the subscription login, so only when chosen
		envFrom = append(envFrom, from(authSecret(t.OwnerID)))
	}
	uid := int64(1000)
	grace := int64(90) // the runner stops claude gracefully; killing it mid token refresh logs the user out
	labels := map[string]string{"banda.dev/thread": t.ID}

	return &sbv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: sbv1.SandboxSpec{
			SandboxBlueprint: sbv1.SandboxBlueprint{
				PodTemplate: sbv1.PodTemplate{
					ObjectMeta: sbv1.PodMetadata{Labels: labels},
					Spec: corev1.PodSpec{
						SecurityContext:               &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid},
						TerminationGracePeriodSeconds: &grace,
						Volumes:                       []corev1.Volume{claudeVolume(claudeClaim)},
						Containers: []corev1.Container{{
							Name:            "runner",
							Image:           c.cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             vars,
							EnvFrom:         envFrom,
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
func (c *Client) loginSandbox(owner, claudeClaim, token string) *sbv1.Sandbox {
	uid := int64(1000)
	grace := int64(90)
	labels := map[string]string{"banda.dev/login": hash(owner)}
	return &sbv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "login-" + hash(owner), Labels: labels},
		Spec: sbv1.SandboxSpec{
			SandboxBlueprint: sbv1.SandboxBlueprint{
				PodTemplate: sbv1.PodTemplate{
					ObjectMeta: sbv1.PodMetadata{Labels: labels},
					Spec: corev1.PodSpec{
						SecurityContext:               &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid},
						TerminationGracePeriodSeconds: &grace,
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
								{Name: "BANDA_RUNNER_TOKEN", Value: token}, // ponytail: plain env on the CR, like the thread token
							},
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
