package kube

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

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
	restConfig, err = rest.InClusterConfig()
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

// ExecutorPod represents a running pod that the agent can exec commands into.
type ExecutorPod struct {
	Name      string
	Namespace string
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
	podName = fmt.Sprintf("%s-%d", podName, time.Now().Unix())

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
			Container: "executor",
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
