// main.go: kubectl-npu plugin — NPU Operator CLI
// 상세: kubectl npu upgrade/status/driver-version 명령을 제공
// 생성일: 2026-03-25
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	dipGVR = schema.GroupVersionResource{Group: "npu.ai", Version: "v1alpha1", Resource: "driverinstallpolicies"}
	ndrGVR = schema.GroupVersionResource{Group: "npu.ai", Version: "v1alpha1", Resource: "nodedevicereports"}
	ncpGVR = schema.GroupVersionResource{Group: "npu.ai", Version: "v1alpha1", Resource: "npuclusterpolicies"}
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "status":
		cmdStatus()
	case "upgrade":
		cmdUpgrade(os.Args[2:])
	case "driver-version":
		cmdDriverVersion()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`kubectl-npu: NPU Operator CLI Plugin

Usage:
  kubectl npu status                          Show all driver policies and node device reports
  kubectl npu driver-version                  Show installed driver versions per node
  kubectl npu upgrade <vendor> --version VER  Upgrade driver to specified version
  kubectl npu upgrade <vendor> --version VER --force  Force upgrade (drain even if blocked)
  kubectl npu upgrade <vendor> --version VER --auto   Enable autoUpgrade + drainEnabled

Vendors: nvidia, furiosa

Examples:
  kubectl npu status
  kubectl npu driver-version
  kubectl npu upgrade nvidia --version 580.126.10 --auto
  kubectl npu upgrade nvidia --version 580.126.10 --force
  kubectl npu upgrade furiosa --version 1.8.0 --auto`)
}

func getClient() dynamic.Interface {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
		os.Exit(1)
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}
	return client
}

func cmdStatus() {
	client := getClient()
	ctx := context.Background()

	fmt.Println("=== DriverInstallPolicies ===")
	dips, err := client.Resource(dipGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing policies: %v\n", err)
	} else {
		fmt.Printf("%-20s %-10s %-10s %-15s\n", "NAME", "VENDOR", "MODEL", "VERSION")
		for _, item := range dips.Items {
			spec := item.Object["spec"].(map[string]interface{})
			driver := spec["driver"].(map[string]interface{})
			fmt.Printf("%-20s %-10s %-10s %-15s\n",
				item.GetName(),
				getStr(spec, "vendor"),
				getStr(spec, "model"),
				getStr(driver, "version"))
		}
	}

	fmt.Println("\n=== NodeDeviceReports ===")
	ndrs, err := client.Resource(ndrGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing NDRs: %v\n", err)
	} else {
		fmt.Printf("%-15s %-10s %-10s %-6s %-8s %-15s\n", "NODE", "VENDOR", "MODEL", "COUNT", "LOADED", "VERSION")
		for _, item := range ndrs.Items {
			status, ok := item.Object["status"].(map[string]interface{})
			if !ok {
				continue
			}
			devices, ok := status["devices"].([]interface{})
			if !ok {
				continue
			}
			for _, d := range devices {
				dev := d.(map[string]interface{})
				loaded := "false"
				if v, ok := dev["driverLoaded"].(bool); ok && v {
					loaded = "true"
				}
				count := int64(0)
				if v, ok := dev["count"].(int64); ok {
					count = v
				} else if v, ok := dev["count"].(float64); ok {
					count = int64(v)
				}
				fmt.Printf("%-15s %-10s %-10s %-6d %-8s %-15s\n",
					item.GetName(),
					getStr(dev, "vendor"),
					getStr(dev, "model"),
					count,
					loaded,
					getStr(dev, "driverVersion"))
			}
		}
	}

	fmt.Println("\n=== NPUClusterPolicy ===")
	ncps, err := client.Resource(ncpGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing policies: %v\n", err)
	} else {
		for _, item := range ncps.Items {
			status, ok := item.Object["status"].(map[string]interface{})
			if !ok {
				fmt.Printf("  %s/%s: no status\n", item.GetNamespace(), item.GetName())
				continue
			}
			conditions, ok := status["conditions"].([]interface{})
			if !ok || len(conditions) == 0 {
				fmt.Printf("  %s/%s: no conditions\n", item.GetNamespace(), item.GetName())
				continue
			}
			cond := conditions[0].(map[string]interface{})
			fmt.Printf("  %s/%s: %s=%s (%s)\n",
				item.GetNamespace(), item.GetName(),
				getStr(cond, "type"), getStr(cond, "status"), getStr(cond, "reason"))
		}
	}
}

func cmdDriverVersion() {
	client := getClient()
	ctx := context.Background()

	ndrs, err := client.Resource(ndrGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-15s %-10s %-10s %-15s %-8s\n", "NODE", "VENDOR", "MODEL", "DRIVER_VER", "LOADED")
	for _, item := range ndrs.Items {
		status, ok := item.Object["status"].(map[string]interface{})
		if !ok {
			continue
		}
		devices, ok := status["devices"].([]interface{})
		if !ok {
			continue
		}
		for _, d := range devices {
			dev := d.(map[string]interface{})
			loaded := "false"
			if v, ok := dev["driverLoaded"].(bool); ok && v {
				loaded = "true"
			}
			fmt.Printf("%-15s %-10s %-10s %-15s %-8s\n",
				item.GetName(),
				getStr(dev, "vendor"),
				getStr(dev, "model"),
				getStr(dev, "driverVersion"),
				loaded)
		}
	}
}

func cmdUpgrade(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu upgrade <vendor> --version <ver> [--force] [--auto]")
		os.Exit(1)
	}

	vendor := args[0]
	version := ""
	force := false
	auto := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--version":
			if i+1 < len(args) {
				version = args[i+1]
				i++
			}
		case "--force":
			force = true
		case "--auto":
			auto = true
		}
	}

	if version == "" {
		fmt.Fprintln(os.Stderr, "Error: --version is required")
		os.Exit(1)
	}

	// Policy 이름 결정
	policyName := ""
	switch strings.ToLower(vendor) {
	case "nvidia":
		policyName = "nvidia-gpu"
	case "furiosa":
		policyName = "furiosa-warboy"
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown vendor '%s'. Use 'nvidia' or 'furiosa'\n", vendor)
		os.Exit(1)
	}

	client := getClient()
	ctx := context.Background()

	// 현재 버전 확인
	dip, err := client.Resource(dipGVR).Get(ctx, policyName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: DriverInstallPolicy '%s' not found: %v\n", policyName, err)
		os.Exit(1)
	}

	spec := dip.Object["spec"].(map[string]interface{})
	driver := spec["driver"].(map[string]interface{})
	currentVer := getStr(driver, "version")

	fmt.Printf("Vendor:          %s\n", vendor)
	fmt.Printf("Policy:          %s\n", policyName)
	fmt.Printf("Current Version: %s\n", currentVer)
	fmt.Printf("Target Version:  %s\n", version)
	fmt.Printf("Auto Upgrade:    %v\n", auto)
	fmt.Printf("Force:           %v\n", force)
	fmt.Println()

	if currentVer == version {
		fmt.Println("Version already set. No change needed.")
		return
	}

	// 패치 구성
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"driver": map[string]interface{}{
				"version": version,
			},
		},
	}

	if auto || force {
		upgradePolicy := map[string]interface{}{
			"autoUpgrade":  true,
			"drainEnabled": true,
		}
		if force {
			upgradePolicy["forceUpgrade"] = true
		}
		patch["spec"].(map[string]interface{})["upgradePolicy"] = upgradePolicy
	}

	patchBytes, _ := json.Marshal(patch)
	_, err = client.Resource(dipGVR).Patch(ctx, policyName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error patching policy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ DriverInstallPolicy '%s' patched: %s → %s\n", policyName, currentVer, version)

	// force annotation
	if force {
		ncps, err := client.Resource(ncpGVR).List(ctx, metav1.ListOptions{})
		if err == nil && len(ncps.Items) > 0 {
			ncp := &ncps.Items[0]
			annot := ncp.GetAnnotations()
			if annot == nil {
				annot = map[string]string{}
			}
			annot["npu.ai/force-upgrade"] = "true"
			ncp.SetAnnotations(annot)
			_, err = client.Resource(ncpGVR).Namespace(ncp.GetNamespace()).Update(ctx, ncp, metav1.UpdateOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to set force annotation: %v\n", err)
			} else {
				fmt.Println("✓ Force upgrade annotation set on NPUClusterPolicy")
			}
		}
	}

	fmt.Println()
	fmt.Println("Monitor progress:")
	fmt.Println("  kubectl get pods -n kube-system | grep npu-op-installer")
	fmt.Println("  kubectl get events --field-selector reason=UpgradeStarted")
	fmt.Println("  kubectl npu driver-version")
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// Dummy unstructured usage to satisfy import
var _ = unstructured.Unstructured{}
