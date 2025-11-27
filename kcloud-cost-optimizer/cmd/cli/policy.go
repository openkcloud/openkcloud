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

// policyCmd represents the policy command
var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Manage policies",
	Long:  `Create, read, update, and delete policies in the Policy Engine.`,
}

var policyCreateCmd = &cobra.Command{
	Use:   "create [file]",
	Short: "Create a new policy",
	Long:  `Create a new policy from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Read policy file
		policyData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Create policy
		url := fmt.Sprintf("http://%s:%d/api/v1/policies", serverHost, serverPort)
		resp, err := http.Post(url, "application/yaml", strings.NewReader(string(policyData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating policy: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error creating policy: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			if id, ok := result["id"].(string); ok {
				fmt.Printf("Policy created successfully with ID: %s\n", id)
			} else {
				fmt.Println("Policy created successfully")
			}
		}
	},
}

var policyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all policies",
	Long:  `List all policies in the Policy Engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/policies", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing policies: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error listing policies: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var policyGetCmd = &cobra.Command{
	Use:   "get <policy-id>",
	Short: "Get a specific policy",
	Long:  `Get details of a specific policy by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		policyID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/policies/%s", serverHost, serverPort, policyID)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting policy: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting policy: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var policyUpdateCmd = &cobra.Command{
	Use:   "update <policy-id> [file]",
	Short: "Update a policy",
	Long:  `Update an existing policy from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		policyID := args[0]
		filePath := args[1]

		// Read policy file
		policyData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Update policy
		url := fmt.Sprintf("http://%s:%d/api/v1/policies/%s", serverHost, serverPort, policyID)
		req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(policyData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/yaml")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating policy: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error updating policy: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			fmt.Printf("Policy %s updated successfully\n", policyID)
		}
	},
}

var policyDeleteCmd = &cobra.Command{
	Use:   "delete <policy-id>",
	Short: "Delete a policy",
	Long:  `Delete a policy by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		policyID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/policies/%s", serverHost, serverPort, policyID)
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting policy: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error deleting policy: %s\n", string(body))
			os.Exit(1)
		}

		fmt.Printf("Policy %s deleted successfully\n", policyID)
	},
}

func init() {
	rootCmd.AddCommand(policyCmd)

	policyCmd.AddCommand(policyCreateCmd)
	policyCmd.AddCommand(policyListCmd)
	policyCmd.AddCommand(policyGetCmd)
	policyCmd.AddCommand(policyUpdateCmd)
	policyCmd.AddCommand(policyDeleteCmd)
}
