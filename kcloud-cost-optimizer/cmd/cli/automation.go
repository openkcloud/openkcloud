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

// automationCmd represents the automation command
var automationCmd = &cobra.Command{
	Use:   "automation",
	Short: "Manage automation rules",
	Long:  `Create, read, update, and delete automation rules in the Policy Engine.`,
}

var automationCreateCmd = &cobra.Command{
	Use:   "create [file]",
	Short: "Create a new automation rule",
	Long:  `Create a new automation rule from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Read automation rule file
		ruleData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Create automation rule
		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules", serverHost, serverPort)
		resp, err := http.Post(url, "application/yaml", strings.NewReader(string(ruleData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error creating automation rule: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			if id, ok := result["id"].(string); ok {
				fmt.Printf("Automation rule created successfully with ID: %s\n", id)
			} else {
				fmt.Println("Automation rule created successfully")
			}
		}
	},
}

var automationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all automation rules",
	Long:  `List all automation rules in the Policy Engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing automation rules: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error listing automation rules: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var automationGetCmd = &cobra.Command{
	Use:   "get <rule-id>",
	Short: "Get a specific automation rule",
	Long:  `Get details of a specific automation rule by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ruleID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules/%s", serverHost, serverPort, ruleID)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting automation rule: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

var automationUpdateCmd = &cobra.Command{
	Use:   "update <rule-id> [file]",
	Short: "Update an automation rule",
	Long:  `Update an existing automation rule from a YAML or JSON file.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ruleID := args[0]
		filePath := args[1]

		// Read automation rule file
		ruleData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		// Update automation rule
		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules/%s", serverHost, serverPort, ruleID)
		req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(ruleData)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/yaml")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error updating automation rule: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			fmt.Printf("Automation rule %s updated successfully\n", ruleID)
		}
	},
}

var automationDeleteCmd = &cobra.Command{
	Use:   "delete <rule-id>",
	Short: "Delete an automation rule",
	Long:  `Delete an automation rule by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ruleID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules/%s", serverHost, serverPort, ruleID)
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error deleting automation rule: %s\n", string(body))
			os.Exit(1)
		}

		fmt.Printf("Automation rule %s deleted successfully\n", ruleID)
	},
}

var automationEnableCmd = &cobra.Command{
	Use:   "enable <rule-id>",
	Short: "Enable an automation rule",
	Long:  `Enable an automation rule by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ruleID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules/%s/enable", serverHost, serverPort, ruleID)
		resp, err := http.Post(url, "application/json", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error enabling automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error enabling automation rule: %s\n", string(body))
			os.Exit(1)
		}

		fmt.Printf("Automation rule %s enabled successfully\n", ruleID)
	},
}

var automationDisableCmd = &cobra.Command{
	Use:   "disable <rule-id>",
	Short: "Disable an automation rule",
	Long:  `Disable an automation rule by its ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ruleID := args[0]

		url := fmt.Sprintf("http://%s:%d/api/v1/automation/rules/%s/disable", serverHost, serverPort, ruleID)
		resp, err := http.Post(url, "application/json", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error disabling automation rule: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error disabling automation rule: %s\n", string(body))
			os.Exit(1)
		}

		fmt.Printf("Automation rule %s disabled successfully\n", ruleID)
	},
}

var automationStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show automation engine status",
	Long:  `Show the current status of the automation engine.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/api/v1/automation/status", serverHost, serverPort)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting automation status: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error getting automation status: %s\n", string(body))
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

func init() {
	rootCmd.AddCommand(automationCmd)

	automationCmd.AddCommand(automationCreateCmd)
	automationCmd.AddCommand(automationListCmd)
	automationCmd.AddCommand(automationGetCmd)
	automationCmd.AddCommand(automationUpdateCmd)
	automationCmd.AddCommand(automationDeleteCmd)
	automationCmd.AddCommand(automationEnableCmd)
	automationCmd.AddCommand(automationDisableCmd)
	automationCmd.AddCommand(automationStatusCmd)
}
