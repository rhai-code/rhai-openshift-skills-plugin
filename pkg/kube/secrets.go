package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetSecretData reads a secret from the given namespace and returns its data fields as strings.
func GetSecretData(namespace, name string) (map[string]string, error) {
	if clientset == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		result[k] = string(v)
	}
	return result, nil
}

// GetNamespaceAnnotation reads a single annotation from the given namespace.
func GetNamespaceAnnotation(namespace, annotation string) (string, error) {
	if clientset == nil {
		return "", fmt.Errorf("kubernetes client not initialized")
	}
	ns, err := clientset.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return ns.Annotations[annotation], nil
}
