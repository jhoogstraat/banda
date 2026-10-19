// Package sandbox creates one agent-sandbox Sandbox per thread. The home
// directory is a per-thread PVC (the checkout survives pod restarts and
// suspends). Claude Code's config dir is a per-user PVC that only the claude
// binary touches, so a user logs in once for all their threads.
// See docs/research/claude-login.md and docs/research/sandbox-lifecycle.md.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

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

// Ensure gives the thread a freshly started pod. It creates the Sandbox if
// needed; otherwise it resumes it and deletes the pod so the controller
// recreates it on the same home volume.
func (c *Client) Ensure(ctx context.Context, t store.Thread) error {
	claim, err := c.ensureClaudeVolume(ctx, t.OwnerID)
	if err != nil {
		return err
	}
	name := "t-" + t.ID
	sandboxes := c.sbx.AgentsV1beta1().Sandboxes(c.cfg.Namespace)
	_, err = sandboxes.Create(ctx, c.sandbox(name, claim, t), metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	if _, err := sandboxes.Patch(ctx, name, types.MergePatchType, []byte(`{"spec":{"operatingMode":"Running"}}`), metav1.PatchOptions{}); err != nil {
		return err
	}
	err = c.kube.CoreV1().Pods(c.cfg.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// ensureClaudeVolume creates the owner's Claude Code config volume.
// ponytail: RWO works while all pods run on one node (kind); multi-node needs RWX or node pinning.
func (c *Client) ensureClaudeVolume(ctx context.Context, owner string) (string, error) {
	sum := sha256.Sum256([]byte(owner))
	name := "claude-" + hex.EncodeToString(sum[:8])
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

func (c *Client) sandbox(name, claudeClaim string, t store.Thread) *sbv1.Sandbox {
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
	}
	var vars []corev1.EnvVar
	for k, v := range env {
		vars = append(vars, corev1.EnvVar{Name: k, Value: v})
	}
	optional := true
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
						Volumes: []corev1.Volume{{Name: "claude", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claudeClaim}}}},
						Containers: []corev1.Container{{
							Name:            "runner",
							Image:           c.cfg.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             vars,
							// Optional git token for private repos (secret "banda-git", key GIT_TOKEN).
							EnvFrom:      []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "banda-git"}, Optional: &optional}}},
							VolumeMounts: []corev1.VolumeMount{{Name: "home", MountPath: home}, {Name: "claude", MountPath: claudeDir}},
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
