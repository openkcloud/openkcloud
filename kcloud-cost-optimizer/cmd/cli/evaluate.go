package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// evaluateCmd represents the evaluate command
var evaluateCmd = &cobra.Command{
	Use:   "evaluate",
	Short: "Evaluate workloads against policies",
	Long:  `Evaluate workloads against policies to generate decisions and recommendations.`,
}

var evaluateWorkloadCmd = &cobra.Command{
	Use:   "workload <workload-id>",
	Short: "Evaluate a workload against all applicable policies",
	Long:  `Evaluate a specific workload against all applicable policies and return decisions.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workloadID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/evaluations/workload/%s", serverHost, serverPort, workloadID)
		resp, err := http.Post(url, "application/json", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error evaluating workload: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error evaluating workload: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var evaluatePolicyCmd = &cobra.Command{
	Use:   "policy <policy-id> <workload-id>",
	Short: "Evaluate a specific policy against a workload",
	Long:  `Evaluate a specific policy against a workload and return the decision.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		policyID := args[0]
		workloadID := args[1]

		url := fmt.Sprintf("http://%s:%d/api/v1/evaluations/policy/%s/workload/%s", serverHost, serverPort, policyID, workloadID)
		resp, err := http.Post(url, "application/json", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error evaluating policy: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error evaluating policy: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var evaluateBatchCmd = &cobra.Command{
	Use:   "batch [file]",
	Short: "Evaluate multiple workloads in batch",
	Long:  `Evaluate multiple workloads from a file containing workload IDs or definitions.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Read batch file
		batchData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Evaluate batch
		url := fmt.Sprintf("http://%s:%d/api/v1/evaluations/batch", serverHost, serverPort)
		resp, err := http.Post(url, "application/yaml", strings.NewReader(string(batchData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error evaluating batch: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error evaluating batch: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var evaluateHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show evaluation history",
	Long:  `Show the history of evaluations performed by the Policy Engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/evaluations/history", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting evaluation history: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting evaluation history: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var evaluateStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show evaluation statistics",
	Long:  `Show statistics about evaluations performed by the Policy Engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/evaluations/stats", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting evaluation stats: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting evaluation stats: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

func init() {
	rootCmd.AddCommand(evaluateCmd)

	evaluateCmd.AddCommand(evaluateWorkloadCmd)
	evaluateCmd.AddCommand(evaluatePolicyCmd)
	evaluateCmd.AddCommand(evaluateBatchCmd)
	evaluateCmd.AddCommand(evaluateHistoryCmd)
	evaluateCmd.AddCommand(evaluateStatsCmd)
}
