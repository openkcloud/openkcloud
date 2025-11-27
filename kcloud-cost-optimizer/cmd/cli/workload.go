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

// workloadCmd represents the workload command
var workloadCmd = &cobra.Command{
	Use:   "workload",
	Short: "Manage workloads",
	Long:  `Create, read, update, and delete workloads in the Policy Engine.`,
}

var workloadCreateCmd = &cobra.Command{
	Use:   "create [file]",
	Short: "Create a new workload",
	Long:  `Create a new workload from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Read workload file
		workloadData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Create workload
		url := fmt.Sprintf("http://%s:%d/api/v1/workloads", serverHost, serverPort)
		resp, err := http.Post(url, "application/yaml", strings.NewReader(string(workloadData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating workload: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error creating workload: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			if id, ok := result["id"].(string); ok {
				fmt.Printf("Workload created successfully with ID: %s\n", id)
			} else {
				fmt.Println("Workload created successfully")
			}
		}
	},
}

var workloadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workloads",
	Long:  `List all workloads in the Policy Engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/workloads", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing workloads: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error listing workloads: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var workloadGetCmd = &cobra.Command{
	Use:   "get <workload-id>",
	Short: "Get a specific workload",
	Long:  `Get details of a specific workload by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workloadID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/workloads/%s", serverHost, serverPort, workloadID)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting workload: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting workload: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var workloadUpdateCmd = &cobra.Command{
	Use:   "update <workload-id> [file]",
	Short: "Update a workload",
	Long:  `Update an existing workload from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		workloadID := args[0]
		filePath := args[1]

		// Read workload file
		workloadData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Update workload
		url := fmt.Sprintf("http://%s:%d/api/v1/workloads/%s", serverHost, serverPort, workloadID)
		req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(workloadData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/yaml")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating workload: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error updating workload: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			fmt.Printf("Workload %s updated successfully\n", workloadID)
		}
	},
}

var workloadDeleteCmd = &cobra.Command{
	Use:   "delete <workload-id>",
	Short: "Delete a workload",
	Long:  `Delete a workload by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workloadID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/workloads/%s", serverHost, serverPort, workloadID)
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting workload: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error deleting workload: %s\n", string(body))
			os.Exit(1)
		}

		fmt.Printf("Workload %s deleted successfully\n", workloadID)
	},
}

func init() {
	rootCmd.AddCommand(workloadCmd)

	workloadCmd.AddCommand(workloadCreateCmd)
	workloadCmd.AddCommand(workloadListCmd)
	workloadCmd.AddCommand(workloadGetCmd)
	workloadCmd.AddCommand(workloadUpdateCmd)
	workloadCmd.AddCommand(workloadDeleteCmd)
}
