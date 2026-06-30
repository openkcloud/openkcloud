/*
DefaultBinder Plugin

This plugin provides the default logic to bind a Pod to a Node using the Kubernetes API.
*/
package plugin

import (
	"bytes"
	"crypto/tls"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const DefaultBinderName = "DefaultBinder"

type DefaultBinder struct {
	client     kubernetes.Interface
	httpClient *http.Client
}

var _ framework.BindPlugin = &DefaultBinder{}

func NewDefaultBinder(client kubernetes.Interface) *DefaultBinder {
	return &DefaultBinder{
		client: client,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

func (d *DefaultBinder) Name() string {
	return DefaultBinderName
}

func (d *DefaultBinder) Bind(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status {
	logger := klog.FromContext(ctx)

	if hasGPURequest(pod) {
		klog.V(2).InfoS("Delegating GPU pod binding to HAMi Extender", "pod", klog.KObj(pod), "node", nodeName)
		
		args := ExtenderBindingArgs{
			PodName:      pod.Name,
			PodNamespace: pod.Namespace,
			PodUID:       string(pod.UID),
			Node:         nodeName,
		}
		
		payload, _ := json.Marshal(args)
		url := "https://hami-scheduler.kube-system.svc.cluster.local:443/bind"
		
		resp, err := d.httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
		if err == nil && resp.StatusCode == http.StatusOK {
			klog.V(2).InfoS("Successfully delegated bind to HAMi Extender", "pod", klog.KObj(pod))
			resp.Body.Close()
			return utils.NewStatus(utils.Success, "")
		}
		
		if err != nil {
			klog.Warningf("HAMi Extender bind failed (TLS/Network): %v, falling back to default bind", err)
		} else {
			klog.Warningf("HAMi Extender bind returned status %d, falling back to default bind", resp.StatusCode)
			resp.Body.Close()
		}
	}

	logger.V(3).Info("Attempting to bind pod to node via direct API", "pod", klog.KObj(pod), "node", nodeName)

	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       pod.UID,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}

	err := d.client.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
	if err != nil {
		logger.Error(err, "Failed to bind pod", "pod", klog.KObj(pod), "node", nodeName)
		return utils.NewStatus(utils.Error, fmt.Sprintf("binding rejected: %v", err))
	}

	logger.V(2).Info("Successfully bound pod to node", "pod", klog.KObj(pod), "node", nodeName)
	return utils.NewStatus(utils.Success, "")
}
