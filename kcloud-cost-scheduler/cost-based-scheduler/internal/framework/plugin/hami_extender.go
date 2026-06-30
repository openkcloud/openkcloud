/*
Hardware Aware Metrics Integration Extender

This module integrates with the HAMI scheduler extender to account for
advanced hardware topologies.
*/
package plugin

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil" // Use ioutil for Go < 1.16, or io.ReadAll for Go >= 1.16
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

type ExtenderArgs struct {
	Pod       *v1.Pod    `json:"pod"`
	Nodes     []*v1.Node `json:"nodes,omitempty"`
	NodeNames []string   `json:"nodeNames,omitempty"`
}

type ExtenderFilterResult struct {
	Nodes       *v1.NodeList `json:"nodes,omitempty"`
	NodeNames   *[]string    `json:"nodeNames,omitempty"`
	FailedNodes map[string]string `json:"failedNodes,omitempty"`
	Error       string       `json:"error,omitempty"`
}

type ExtenderBindingArgs struct {
	PodName      string `json:"podName"`
	PodNamespace string `json:"podNamespace"`
	PodUID       string `json:"podUID"`
	Node         string `json:"node"`
}

type HAMiExtenderClient struct {
	baseURL    string
	httpClient *http.Client
	retries    int
	retryDelay time.Duration
}

func NewHAMiExtenderClient() *HAMiExtenderClient {
	return &HAMiExtenderClient{
		baseURL: "https://10.109.140.73:443", // TODO: Base URL should be configurable
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		retries:    3,             // Default retries
		retryDelay: 1 * time.Second, // Default retry delay
	}
}

func (c *HAMiExtenderClient) doRequest(method, path string, payload []byte) ([]byte, error) {
	var resp *http.Response
	var err error
	url := c.baseURL + path

	for i := 0; i <= c.retries; i++ {
		req, _ := http.NewRequest(method, url, bytes.NewBuffer(payload))
		req.Header.Set("Content-Type", "application/json")

		resp, err = c.httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}

		klog.V(4).InfoS("HAMi Extender request failed, retrying...",
			"attempt", i+1, "maxAttempts", c.retries+1,
			"url", url, "error", err, "statusCode", resp.StatusCode)

		if i < c.retries {
			time.Sleep(c.retryDelay)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("HAMi Extender request failed after %d retries: %w", c.retries+1, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body) // Read body for detailed error
		return nil, fmt.Errorf("HAMi Extender returned non-OK status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return bodyBytes, nil
}


func (c *HAMiExtenderClient) Filter(pod *v1.Pod, nodes []*v1.Node) ([]*v1.Node, error) {
	if !hasGPURequest(pod) {
		return nodes, nil
	}

	nodeNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeNames = append(nodeNames, n.Name)
	}

	args := ExtenderArgs{
		Pod:       pod,
		NodeNames: nodeNames,
	}

	payload, _ := json.Marshal(args)
	
	respBody, err := c.doRequest("POST", "/filter", payload)
	if err != nil {
		klog.Warningf("HAMi Extender filter failed (falling back to default scheduler): %v", err)
		return nodes, nil // Fallback to default scheduler by not filtering
	}

	var result ExtenderFilterResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		klog.Warningf("Failed to decode HAMi Extender filter response: %v", err)
		return nodes, nil
	}

	if result.NodeNames == nil {
		return nodes, nil
	}

	filteredNodes := make([]*v1.Node, 0)
	keepSet := make(map[string]bool)
	for _, name := range *result.NodeNames {
		keepSet[name] = true
	}

	for _, n := range nodes {
		if keepSet[n.Name] {
			filteredNodes = append(filteredNodes, n)
		}
	}

	return filteredNodes, nil
}

func (c *HAMiExtenderClient) Bind(pod *v1.Pod, node string) error {
	args := ExtenderBindingArgs{
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		PodUID:       string(pod.UID),
		Node:         node,
	}

	payload, _ := json.Marshal(args)
	respBody, err := c.doRequest("POST", "/bind", payload)
	if err != nil {
		return fmt.Errorf("HAMi Extender bind failed: %w", err)
	}

	klog.V(4).InfoS("HAMi Extender bind successful", "pod", klog.KObj(pod), "node", node, "response", string(respBody))
	return nil
}

func hasGPURequest(pod *v1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if _, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			return true
		}
	}
	return false
}
