package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Policy Engine status",
	Long:  `Check the health and status of the Policy Engine service.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/health", serverHost, serverPort)

		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		resp, err := client.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error connecting to Policy Engine: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Policy Engine is not healthy (status: %d)\n", resp.StatusCode)
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		if verbose {
			jsonData, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(jsonData))
		} else {
			if status, ok := result["status"].(string); ok {
				fmt.Printf("Policy Engine Status: %s\n", status)
			}
			if version, ok := result["version"].(string); ok {
				fmt.Printf("Version: %s\n", version)
			}
			if uptime, ok := result["uptime"].(string); ok {
				fmt.Printf("Uptime: %s\n", uptime)
			}
		}
	},
}

// metricsCmd represents the metrics command
var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Show Policy Engine metrics",
	Long:  `Show metrics from the Policy Engine service.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/metrics", serverHost, serverPort)

		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		resp, err := client.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting metrics: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error getting metrics (status: %d)\n", resp.StatusCode)
			os.Exit(1)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading metrics response: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(string(body))
	},
}

// infoCmd represents the info command
var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show Policy Engine information",
	Long:  `Show detailed information about the Policy Engine service.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/info", serverHost, serverPort)

		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		resp, err := client.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting info: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Error getting info (status: %d)\n", resp.StatusCode)
			os.Exit(1)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonData))
	},
}

// pingCmd represents the ping command
var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Ping the Policy Engine",
	Long:  `Ping the Policy Engine to check connectivity.`,
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("http://%s:%d/ping", serverHost, serverPort)

		client := &http.Client{
			Timeout: 5 * time.Second,
		}

		start := time.Now()
		resp, err := client.Get(url)
		duration := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Ping failed: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Ping failed (status: %d)\n", resp.StatusCode)
			os.Exit(1)
		}

		fmt.Printf("Ping successful - Response time: %v\n", duration)

		if verbose {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("Response: %s\n", string(body))
		}
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(metricsCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(pingCmd)
}
