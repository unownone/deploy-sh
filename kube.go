package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Configuration variables
const (
	namespace       = "default"           // Kubernetes namespace
	volumeMountName = "dockerfile-config" // Name of the ConfigMap
	secretName      = "dockercred"        // Name of the secret containing Docker credentials
	podNamePrefix   = "kaniko-pod-"       // Prefix for the Kaniko pod name
	nameLength      = 10                  // Length of the random string for pod name
)

func BuildKaniko(repo, tag string) error {
	// Load the current kubectl configuration
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	// Get the current context
	rawConfig, err := kubeConfig.RawConfig()
	if err != nil {
		return fmt.Errorf("failed to get raw config: %v", err)
	}

	currentContext := rawConfig.CurrentContext
	fmt.Printf("Using Kubernetes context: %s\n", currentContext)

	// Get the rest config
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to get client config: %v", err)
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %v", err)
	}

	// Extract username from secret
	username, err := getDockerUsername(clientset, namespace)
	if err != nil {
		return fmt.Errorf("failed to get Docker username: %v", err)
	}

	// Generate a unique name for the Kaniko pod
	podName := generateRandomName(podNamePrefix, nameLength)

	// Apply the Pod
	if err := applyPod(clientset, namespace, podName, volumeMountName, username, repo, tag); err != nil {
		return fmt.Errorf("failed to apply Pod: %v", err)
	}

	// Wait for Pod to complete and stream logs
	podStatus, err := waitForPodCompletionAndStreamLogs(clientset, namespace, podName)
	if err != nil {
		return fmt.Errorf("failed to monitor Pod: %v", err)
	}

	// Print build status
	if podStatus == v1.PodSucceeded {
		fmt.Println("Build uploaded successfully.")
	} else {
		fmt.Println("Build failed.")
	}

	// Clean up resources
	// if err := deletePod(clientset, namespace, podName); err != nil {
	// 	return fmt.Errorf("failed to delete Pod: %v", err)
	// }

	return nil
}

func generateRandomName(prefix string, length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	var name strings.Builder
	name.WriteString(prefix)
	for i := 0; i < length; i++ {
		randIndex, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		name.WriteByte(chars[randIndex.Int64()])
	}
	return name.String()
}

func createConfigMap(clientset *kubernetes.Clientset, namespace, name, dir string) error {
	files := map[string]string{}
	err := filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files[relPath] = string(content)
		}
		return nil
	})
	if err != nil {
		return err
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Data: files,
	}

	_, err = clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
	return err
}

func applyPod(clientset *kubernetes.Clientset, namespace, podName, volumeMountName, userName, repo, tag string) error {
	dir, err := os.Getwd()
	fmt.Printf("Mounting current directory %s\n", dir)
	if err != nil {
		return err
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "kaniko",
					Image: "gcr.io/kaniko-project/executor:latest",
					Args: []string{
						"--dockerfile=/workspace/Dockerfile",
						"--context=dir:///workspace/",
						fmt.Sprintf("--destination=%s/%s:%s", userName, repo, tag),
					},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      volumeMountName,
							MountPath: "/workspace",
						},
						{
							Name:      "kaniko-secret",
							MountPath: "/kaniko/.docker",
						},
					},
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
			Volumes: []v1.Volume{
				{
					Name: volumeMountName,
					VolumeSource: v1.VolumeSource{
						ConfigMap: &v1.ConfigMapVolumeSource{
							LocalObjectReference: v1.LocalObjectReference{
								Name: dir,
							},
						},
					},
				},
				{
					Name: "kaniko-secret",
					VolumeSource: v1.VolumeSource{
						Secret: &v1.SecretVolumeSource{
							SecretName: secretName,
							Items: []v1.KeyToPath{
								{
									Key:  ".dockerconfigjson",
									Path: "config.json",
								},
							},
						},
					},
				},
			},
		},
	}

	pod, err = clientset.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("Pod %s[%s] created.\n", pod.Name, pod.Status.Phase)
	return nil
}

func waitForPodCompletionAndStreamLogs(clientset *kubernetes.Clientset, namespace, name string) (v1.PodPhase, error) {
	var status v1.PodPhase
	for {
		pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return status, err
		}

		status = pod.Status.Phase

		if status == v1.PodSucceeded || status == v1.PodFailed {
			break
		}

		// Stream logs if the pod is running
		if status == v1.PodRunning {
			fmt.Printf("Pod %s is running...\n", name)
			logs, err := clientset.CoreV1().Pods(namespace).GetLogs(name, &v1.PodLogOptions{
				Follow: true,
			}).Stream(context.TODO())
			if err != nil {
				return status, err
			}
			defer logs.Close()
			fmt.Printf("Logs for %s:\n", name)
		}

		time.Sleep(10 * time.Second)
	}

	return status, nil
}

func deletePod(clientset *kubernetes.Clientset, namespace, name string) error {
	return clientset.CoreV1().Pods(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func deleteConfigMap(clientset *kubernetes.Clientset, namespace, name string) error {
	return clientset.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func getDockerUsername(clientset *kubernetes.Clientset, namespace string) (string, error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	data, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return "", fmt.Errorf("dockercred secret does not contain .dockerconfigjson")
	}

	// Extract username from Docker config JSON
	var dockerConfig map[string]interface{}
	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return "", err
	}

	auths, ok := dockerConfig["auths"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("auths not found in Docker config")
	}

	// Assuming you need to extract the username for a specific registry
	for _, auth := range auths {
		authMap, ok := auth.(map[string]interface{})
		if ok {
			username, _ := authMap["username"].(string)
			return username, nil
		}
	}

	return "", fmt.Errorf("username not found in Docker config")
}
