package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile    string
	verbose    bool
	configPath string
	logLevel   string
	serverHost string
	serverPort int
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "policy-cli",
	Short: "Policy Engine CLI - Command line interface for Policy Engine",
	Long: `Policy Engine CLI provides a command line interface for managing
policies, workloads, evaluations, and automation rules.

The CLI supports various operations including:
- Policy management (create, read, update, delete)
- Workload management and evaluation
- Automation rule configuration
- System health and status checks
- Configuration management`,
	Version: "1.0.0",
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.policy-cli.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&configPath, "config-path", "./config/config.yaml", "path to config file")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&serverHost, "server-host", "localhost", "server host")
	rootCmd.PersistentFlags().IntVar(&serverPort, "server-port", 8080, "server port")

	// Bind flags to viper
	viper.BindPFlag("server.host", rootCmd.PersistentFlags().Lookup("server-host"))
	viper.BindPFlag("server.port", rootCmd.PersistentFlags().Lookup("server-port"))
	viper.BindPFlag("logging.level", rootCmd.PersistentFlags().Lookup("log-level"))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".policy-cli" (without extension).
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".policy-cli")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil && verbose {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
