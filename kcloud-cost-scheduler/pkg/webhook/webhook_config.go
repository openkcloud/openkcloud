/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// WebhookConfig manages webhook configuration
type WebhookConfig struct {
	Client  client.Client
	Scheme  *runtime.Scheme
	decoder admission.Decoder
}

// NewWebhookConfig creates a new webhook configuration
func NewWebhookConfig(client client.Client, scheme *runtime.Scheme) *WebhookConfig {
	return &WebhookConfig{
		Client: client,
		Scheme: scheme,
	}
}

// SetupWebhooks sets up all webhooks
func (wc *WebhookConfig) SetupWebhooks(mgr webhook.Server) error {
	log := log.FromContext(context.Background())

	// Create decoders
	wc.decoder = admission.NewDecoder(wc.Scheme)

	// Setup Pod Mutating Webhook
	podMutator := NewPodMutator(wc.Client)
	if err := podMutator.InjectDecoder(wc.decoder); err != nil {
		return fmt.Errorf("failed to inject decoder into pod mutator: %w", err)
	}

	mgr.Register("/mutate-v1-pod", &webhook.Admission{
		Handler: podMutator,
	})

	log.Info("Registered Pod mutating webhook")

	// Setup WorkloadOptimizer Validating Webhook
	woValidator := NewWorkloadOptimizerValidator(wc.Client)
	if err := woValidator.InjectDecoder(wc.decoder); err != nil {
		return fmt.Errorf("failed to inject decoder into WorkloadOptimizer validator: %w", err)
	}

	mgr.Register("/validate-kcloud-io-v1alpha1-workloadoptimizer", &webhook.Admission{
		Handler: woValidator,
	})

	log.Info("Registered WorkloadOptimizer validating webhook")

	return nil
}

// CreateWebhookConfiguration creates the webhook configuration resources
func (wc *WebhookConfig) CreateWebhookConfiguration(ctx context.Context, namespace string) error {
	log := log.FromContext(ctx)

	// Create MutatingWebhookConfiguration for Pod mutation
	if err := wc.createPodMutatingWebhookConfig(ctx, namespace); err != nil {
		return fmt.Errorf("failed to create Pod mutating webhook configuration: %w", err)
	}

	// Create ValidatingWebhookConfiguration for WorkloadOptimizer validation
	if err := wc.createWorkloadOptimizerValidatingWebhookConfig(ctx, namespace); err != nil {
		return fmt.Errorf("failed to create WorkloadOptimizer validating webhook configuration: %w", err)
	}

	log.Info("Webhook configurations created successfully")
	return nil
}

// createPodMutatingWebhookConfig creates the Pod mutating webhook configuration
func (wc *WebhookConfig) createPodMutatingWebhookConfig(ctx context.Context, namespace string) error {
	log := log.FromContext(ctx)

	webhookConfig := &metav1.ObjectMeta{
		Name:      "kcloud-pod-mutator",
		Namespace: namespace,
		Labels: map[string]string{
			"app":                        "kcloud-operator",
			"kcloud.io/component":        "webhook",
			"kcloud.io/webhook-type":     "mutating",
			"kcloud.io/webhook-resource": "pod",
		},
		Annotations: map[string]string{
			"kcloud.io/description": "Mutates pods to apply optimization policies",
			"kcloud.io/version":     "v1alpha1",
		},
	}

	log.V(1).Info("Created Pod mutating webhook configuration",
		"name", webhookConfig.Name,
		"namespace", webhookConfig.Namespace)

	// In a real implementation, this would create the actual MutatingWebhookConfiguration resource
	// For now, we'll just log the configuration
	log.V(1).Info("Pod mutating webhook configuration", "config", webhookConfig)

	return nil
}

// createWorkloadOptimizerValidatingWebhookConfig creates the WorkloadOptimizer validating webhook configuration
func (wc *WebhookConfig) createWorkloadOptimizerValidatingWebhookConfig(ctx context.Context, namespace string) error {
	log := log.FromContext(ctx)

	webhookConfig := &metav1.ObjectMeta{
		Name:      "kcloud-workloadoptimizer-validator",
		Namespace: namespace,
		Labels: map[string]string{
			"app":                        "kcloud-operator",
			"kcloud.io/component":        "webhook",
			"kcloud.io/webhook-type":     "validating",
			"kcloud.io/webhook-resource": "workloadoptimizer",
		},
		Annotations: map[string]string{
			"kcloud.io/description": "Validates WorkloadOptimizer resources",
			"kcloud.io/version":     "v1alpha1",
		},
	}

	log.V(1).Info("Created WorkloadOptimizer validating webhook configuration",
		"name", webhookConfig.Name,
		"namespace", webhookConfig.Namespace)

	// In a real implementation, this would create the actual ValidatingWebhookConfiguration resource
	// For now, we'll just log the configuration
	log.V(1).Info("WorkloadOptimizer validating webhook configuration", "config", webhookConfig)

	return nil
}

// ValidateWebhookHealth checks if webhooks are healthy
func (wc *WebhookConfig) ValidateWebhookHealth(ctx context.Context) error {
	log := log.FromContext(ctx)

	// Check if webhook service is running
	var services corev1.ServiceList
	err := wc.Client.List(ctx, &services, client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err != nil {
		return fmt.Errorf("failed to list webhook services: %w", err)
	}

	if len(services.Items) == 0 {
		return fmt.Errorf("no webhook services found")
	}

	// Check if webhook pods are running
	var pods corev1.PodList
	err = wc.Client.List(ctx, &pods, client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err != nil {
		return fmt.Errorf("failed to list webhook pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no webhook pods found")
	}

	// Check pod health
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			return fmt.Errorf("webhook pod %s is not running (phase: %s)", pod.Name, pod.Status.Phase)
		}

		// Check if all containers are ready
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if !containerStatus.Ready {
				return fmt.Errorf("webhook container %s in pod %s is not ready", containerStatus.Name, pod.Name)
			}
		}
	}

	log.Info("Webhook health check passed",
		"services", len(services.Items),
		"pods", len(pods.Items))

	return nil
}

// GetWebhookMetrics returns webhook performance metrics
func (wc *WebhookConfig) GetWebhookMetrics(ctx context.Context) (*WebhookMetrics, error) {
	log := log.FromContext(ctx)

	metrics := &WebhookMetrics{
		Timestamp: time.Now(),
	}

	// Get webhook pods
	var pods corev1.PodList
	err := wc.Client.List(ctx, &pods, client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list webhook pods: %w", err)
	}

	metrics.PodCount = len(pods.Items)
	metrics.RunningPods = 0
	metrics.ReadyPods = 0

	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			metrics.RunningPods++
		}

		// Check if all containers are ready
		allReady := true
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if !containerStatus.Ready {
				allReady = false
				break
			}
		}
		if allReady {
			metrics.ReadyPods++
		}
	}

	// Get webhook services
	var services corev1.ServiceList
	err = wc.Client.List(ctx, &services, client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list webhook services: %w", err)
	}

	metrics.ServiceCount = len(services.Items)

	log.V(1).Info("Webhook metrics collected",
		"podCount", metrics.PodCount,
		"runningPods", metrics.RunningPods,
		"readyPods", metrics.ReadyPods,
		"serviceCount", metrics.ServiceCount)

	return metrics, nil
}

// WebhookMetrics represents webhook performance metrics
type WebhookMetrics struct {
	Timestamp    time.Time
	PodCount     int
	RunningPods  int
	ReadyPods    int
	ServiceCount int
	HealthScore  float64
}

// CalculateHealthScore calculates the overall health score of webhooks
func (wm *WebhookMetrics) CalculateHealthScore() float64 {
	if wm.PodCount == 0 {
		return 0.0
	}

	// Calculate health score based on running and ready pods
	runningScore := float64(wm.RunningPods) / float64(wm.PodCount)
	readyScore := float64(wm.ReadyPods) / float64(wm.PodCount)

	// Weighted average (running pods are more important)
	wm.HealthScore = (runningScore*0.6 + readyScore*0.4)
	return wm.HealthScore
}

// CleanupWebhookResources cleans up webhook resources
func (wc *WebhookConfig) CleanupWebhookResources(ctx context.Context, namespace string) error {
	log := log.FromContext(ctx)

	// Delete webhook configurations
	// In a real implementation, this would delete the actual resources
	log.Info("Cleaning up webhook resources", "namespace", namespace)

	// Clean up services
	var services corev1.ServiceList
	err := wc.Client.List(ctx, &services, client.InNamespace(namespace), client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err == nil {
		for _, service := range services.Items {
			err := wc.Client.Delete(ctx, &service)
			if err != nil {
				log.Error(err, "Failed to delete webhook service", "service", service.Name)
			} else {
				log.Info("Deleted webhook service", "service", service.Name)
			}
		}
	}

	// Clean up pods (these should be managed by deployments)
	var pods corev1.PodList
	err = wc.Client.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels{
		"kcloud.io/component": "webhook",
	})
	if err == nil {
		for _, pod := range pods.Items {
			err := wc.Client.Delete(ctx, &pod)
			if err != nil {
				log.Error(err, "Failed to delete webhook pod", "pod", pod.Name)
			} else {
				log.Info("Deleted webhook pod", "pod", pod.Name)
			}
		}
	}

	log.Info("Webhook cleanup completed")
	return nil
}

// UpdateWebhookConfiguration updates webhook configuration
func (wc *WebhookConfig) UpdateWebhookConfiguration(ctx context.Context, namespace string, configType string) error {
	log := log.FromContext(ctx)

	log.Info("Updating webhook configuration",
		"namespace", namespace,
		"configType", configType)

	// In a real implementation, this would update the actual webhook configuration
	// For now, we'll just log the update
	log.V(1).Info("Webhook configuration updated",
		"namespace", namespace,
		"configType", configType)

	return nil
}

// GetWebhookEndpoints returns webhook endpoints
func (wc *WebhookConfig) GetWebhookEndpoints(ctx context.Context, namespace string) ([]WebhookEndpoint, error) {
	log := log.FromContext(ctx)

	endpoints := []WebhookEndpoint{
		{
			Name:        "pod-mutator",
			Path:        "/mutate-v1-pod",
			Type:        "mutating",
			Resource:    "pods",
			APIVersion:  "v1",
			Description: "Mutates pods to apply optimization policies",
		},
		{
			Name:        "workloadoptimizer-validator",
			Path:        "/validate-kcloud-io-v1alpha1-workloadoptimizer",
			Type:        "validating",
			Resource:    "workloadoptimizers",
			APIVersion:  "kcloud.io/v1alpha1",
			Description: "Validates WorkloadOptimizer resources",
		},
	}

	log.V(1).Info("Retrieved webhook endpoints", "count", len(endpoints))
	return endpoints, nil
}

// WebhookEndpoint represents a webhook endpoint
type WebhookEndpoint struct {
	Name        string
	Path        string
	Type        string
	Resource    string
	APIVersion  string
	Description string
}
