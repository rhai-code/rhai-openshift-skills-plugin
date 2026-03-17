package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// RunJob creates a Kubernetes Job that runs the specified container image
// with the skill content passed as an environment variable.
// It waits for the Job to complete and returns the pod logs.
func RunJob(namespace, serviceAccount, containerImage, taskName, skillContent string, timeoutMinutes int) (string, error) {
	if clientset == nil {
		return "", fmt.Errorf("kubernetes client not initialized")
	}
	if timeoutMinutes <= 0 {
		timeoutMinutes = 5
	}

	// Sanitize task name for Kubernetes naming
	jobName := sanitizeName("skill-" + taskName)
	if len(jobName) > 50 {
		jobName = jobName[:50]
	}
	jobName = fmt.Sprintf("%s-%d", jobName, time.Now().Unix())

	backoffLimit := int32(0)
	ttl := int32(600) // cleanup after 10 minutes

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "openshift-skills-plugin",
				"skills-plugin/task":           sanitizeName(taskName),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "skill",
							Image: containerImage,
							Env: []corev1.EnvVar{
								{
									Name:  "SKILL_CONTENT",
									Value: skillContent,
								},
								{
									Name:  "TASK_NAME",
									Value: taskName,
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()

	// Create the Job
	created, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	log.Printf("Created job %s/%s for task %q", namespace, created.Name, taskName)

	// Watch for Job completion
	timeout := time.Duration(timeoutMinutes) * time.Minute
	output, err := waitForJobAndGetLogs(ctx, namespace, created.Name, timeout)
	if err != nil {
		return output, err
	}

	return output, nil
}

func waitForJobAndGetLogs(ctx context.Context, namespace, jobName string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := clientset.BatchV1().Jobs(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + jobName,
	})
	if err != nil {
		return "", fmt.Errorf("watch job: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		if event.Type == watch.Error {
			return "", fmt.Errorf("watch error")
		}
		job, ok := event.Object.(*batchv1.Job)
		if !ok {
			continue
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				logs, _ := getJobPodLogs(ctx, namespace, jobName)
				return logs, nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				logs, _ := getJobPodLogs(ctx, namespace, jobName)
				return logs, fmt.Errorf("job failed: %s", cond.Message)
			}
		}
	}

	// Timeout — try to get whatever logs exist
	logs, _ := getJobPodLogs(ctx, namespace, jobName)
	return logs, fmt.Errorf("job timed out after %v", timeout)
}

func getJobPodLogs(ctx context.Context, namespace, jobName string) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for job %s", jobName)
	}

	var allLogs bytes.Buffer
	for _, pod := range pods.Items {
		req := clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		stream, err := req.Stream(ctx)
		if err != nil {
			allLogs.WriteString(fmt.Sprintf("[error reading logs from %s: %v]\n", pod.Name, err))
			continue
		}
		logBytes, _ := io.ReadAll(stream)
		stream.Close()
		allLogs.Write(logBytes)
	}

	return allLogs.String(), nil
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	var result []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		} else if c == ' ' || c == '_' {
			result = append(result, '-')
		}
	}
	// Trim leading/trailing dashes
	return strings.Trim(string(result), "-")
}
