package kube

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var (
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
)

func Init() error {
	var err error

	tokenPath := os.Getenv("KUBE_SA_TOKEN_PATH")
	caPath := os.Getenv("KUBE_SA_CA_PATH")
	if tokenPath != "" && caPath != "" {
		// Use projected SA token at a non-standard path (hidden from agent shell)
		restConfig, err = buildInClusterConfig(tokenPath, caPath)
	} else {
		restConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return fmt.Errorf("get in-cluster config: %w", err)
	}
	clientset, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	log.Println("Kubernetes client initialized")
	return nil
}

func buildInClusterConfig(tokenPath, caPath string) (*rest.Config, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST/PORT not set")
	}
	return &rest.Config{
		Host:            "https://" + host + ":" + port,
		BearerTokenFile: tokenPath,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: caPath,
		},
	}, nil
}

// ExecutorPod represents a running pod that the agent can exec commands into.
type ExecutorPod struct {
	Name      string
	Namespace string
	Container string // defaults to "executor" if empty
}

func (ep *ExecutorPod) containerName() string {
	if ep.Container != "" {
		return ep.Container
	}
	return "executor"
}

// CreateExecutorPod creates a long-lived pod with the given image and SA
// that sleeps until we exec commands into it. Returns once the pod is Running.
func CreateExecutorPod(namespace, serviceAccount, containerImage, taskName string) (*ExecutorPod, error) {
	if clientset == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}

	podName := sanitizeName("skill-exec-" + taskName)
	if len(podName) > 50 {
		podName = podName[:50]
	}
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	podName = fmt.Sprintf("%s-%d-%s", podName, time.Now().Unix(), hex.EncodeToString(randBytes))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "openshift-skills-plugin",
				"skills-plugin/task":           sanitizeName(taskName),
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccount,
			RestartPolicy:      corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "executor",
					Image:   containerImage,
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}

	ctx := context.Background()
	created, err := clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create executor pod: %w", err)
	}
	log.Printf("Created executor pod %s/%s for task %q", namespace, created.Name, taskName)

	// Wait for pod to be Running
	if err := waitForPodRunning(ctx, namespace, created.Name, 3*time.Minute); err != nil {
		// Clean up on failure
		_ = DeleteExecutorPod(&ExecutorPod{Name: created.Name, Namespace: namespace})
		return nil, fmt.Errorf("executor pod did not start: %w", err)
	}

	return &ExecutorPod{Name: created.Name, Namespace: namespace}, nil
}

// ExecCommand runs a command in the executor pod and returns stdout+stderr.
func ExecCommand(ep *ExecutorPod, command string, timeout time.Duration) (string, error) {
	if clientset == nil || restConfig == nil {
		return "", fmt.Errorf("kubernetes client not initialized")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(ep.Name).
		Namespace(ep.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: ep.containerName(),
			Command:   []string{"sh", "-c", command},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("create executor: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	var result []string
	if err != nil {
		result = append(result, fmt.Sprintf("Error: %v", err))
	}
	if stdout.Len() > 0 {
		out := stdout.String()
		if len(out) > 10000 {
			out = out[:10000] + "\n...(truncated)"
		}
		result = append(result, "STDOUT:\n"+out)
	}
	if stderr.Len() > 0 {
		errOut := stderr.String()
		if len(errOut) > 5000 {
			errOut = errOut[:5000] + "\n...(truncated)"
		}
		result = append(result, "STDERR:\n"+errOut)
	}

	if len(result) == 0 {
		return "Command completed with no output", nil
	}

	output := ""
	for i, r := range result {
		if i > 0 {
			output += "\n"
		}
		output += r
	}
	return output, nil
}

// DeleteExecutorPod removes the executor pod.
func DeleteExecutorPod(ep *ExecutorPod) error {
	if clientset == nil {
		return fmt.Errorf("kubernetes client not initialized")
	}
	ctx := context.Background()
	err := clientset.CoreV1().Pods(ep.Namespace).Delete(ctx, ep.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Failed to delete executor pod %s/%s: %v", ep.Namespace, ep.Name, err)
		return err
	}
	log.Printf("Deleted executor pod %s/%s", ep.Namespace, ep.Name)
	return nil
}

// CheckPodPermissions checks whether the plugin SA can create, exec, and delete pods
// in the given namespace using SelfSubjectAccessReview.
func CheckPodPermissions(namespace string) (canCreate, canExec, canDelete bool) {
	if clientset == nil {
		return false, false, false
	}
	ctx := context.Background()

	checks := []struct {
		resource    string
		subresource string
		verb        string
		result      *bool
	}{
		{"pods", "", "create", &canCreate},
		{"pods", "exec", "create", &canExec},
		{"pods", "", "delete", &canDelete},
	}

	for _, c := range checks {
		sar, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx,
			&authzv1.SelfSubjectAccessReview{
				Spec: authzv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authzv1.ResourceAttributes{
						Namespace:   namespace,
						Verb:        c.verb,
						Group:       "",
						Resource:    c.resource,
						Subresource: c.subresource,
					},
				},
			}, metav1.CreateOptions{})
		if err == nil {
			*c.result = sar.Status.Allowed
		}
	}
	return
}

func waitForPodRunning(ctx context.Context, namespace, podName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + podName,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		if event.Type == watch.Error {
			return fmt.Errorf("watch error")
		}
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		if pod.Status.Phase == corev1.PodRunning {
			return nil
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return fmt.Errorf("pod entered %s phase", pod.Status.Phase)
		}
	}

	return fmt.Errorf("timed out waiting for pod to start")
}
