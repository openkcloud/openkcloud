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
	"io"
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
	Nodes       *v1.NodeList      `json:"nodes,omitempty"`
	NodeNames   *[]string         `json:"nodeNames,omitempty"`
	FailedNodes map[string]string `json:"failedNodes,omitempty"`
	Error       string            `json:"error,omitempty"`
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
		retries:    3,
		retryDelay: 1 * time.Second,
	}
}

// NewHAMiExtenderClientWithHTTP builds a client for tests (fake transport, no sleep retries).
func NewHAMiExtenderClientWithHTTP(httpClient *http.Client, retries int) *HAMiExtenderClient {
	c := NewHAMiExtenderClient()
	if httpClient != nil {
		c.httpClient = httpClient
	}
	c.retries = retries
	c.retryDelay = 0
	return c
}

func (c *HAMiExtenderClient) doRequest(method, path string, payload []byte) ([]byte, error) {
	var resp *http.Response
	var err error
	url := c.baseURL + path

	for i := 0; i <= c.retries; i++ {
		req, reqErr := http.NewRequest(method, url, bytes.NewBuffer(payload))
		if reqErr != nil {
			return nil, fmt.Errorf("HAMi Extender request build failed: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = c.httpClient.Do(req)
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		if err == nil && resp != nil && statusCode == http.StatusOK {
			break
		}

		klog.V(4).InfoS("HAMi Extender request failed, retrying...",
			"attempt", i+1, "maxAttempts", c.retries+1,
			"url", url, "error", err, "statusCode", statusCode)

		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			resp = nil
		}

		if i < c.retries && c.retryDelay > 0 {
			time.Sleep(c.retryDelay)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("HAMi Extender request failed after %d retries: %w", c.retries+1, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("HAMi Extender request failed after %d retries: nil response", c.retries+1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HAMi Extender returned non-OK status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
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

	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("HAMi Extender filter marshal: %w", err)
	}

	respBody, err := c.doRequest("POST", "/filter", payload)
	if err != nil {
		// Caller keeps prior candidates and records hami_filter_error.
		return nil, err
	}

	var result ExtenderFilterResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("HAMi Extender filter decode: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("HAMi Extender filter error: %s", result.Error)
	}

	if result.NodeNames == nil {
		return nodes, nil
	}

	keepSet := make(map[string]bool, len(*result.NodeNames))
	for _, name := range *result.NodeNames {
		keepSet[name] = true
	}

	filteredNodes := make([]*v1.Node, 0, len(nodes))
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

	payload, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("HAMi Extender bind marshal: %w", err)
	}
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
