package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the policy engine
type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Redis      RedisConfig      `mapstructure:"redis"`
	Logging    LoggingConfig    `mapstructure:"logging"`
	Policy     PolicyConfig     `mapstructure:"policy"`
	Automation AutomationConfig `mapstructure:"automation"`
	Monitoring MonitoringConfig `mapstructure:"monitoring"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Port         int           `mapstructure:"port"`
	Host         string        `mapstructure:"host"`
	Debug        bool          `mapstructure:"debug"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Type              string        `mapstructure:"type"`
	Host              string        `mapstructure:"host"`
	Port              int           `mapstructure:"port"`
	Database          string        `mapstructure:"database"`
	Username          string        `mapstructure:"username"`
	Password          string        `mapstructure:"password"`
	SSLMode           string        `mapstructure:"ssl_mode"`
	MaxConnections    int           `mapstructure:"max_connections"`
	ConnectionTimeout time.Duration `mapstructure:"connection_timeout"`
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	Password     string        `mapstructure:"password"`
	Database     int           `mapstructure:"database"`
	PoolSize     int           `mapstructure:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	FilePath   string `mapstructure:"file_path"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

// PolicyConfig holds policy engine configuration
type PolicyConfig struct {
	CacheTTL           time.Duration `mapstructure:"cache_ttl"`
	MaxPolicies        int           `mapstructure:"max_policies"`
	EvaluationTimeout  time.Duration `mapstructure:"evaluation_timeout"`
	ConflictResolution string        `mapstructure:"conflict_resolution"`
}

// AutomationConfig holds automation engine configuration
type AutomationConfig struct {
	Enabled            bool          `mapstructure:"enabled"`
	CheckInterval      time.Duration `mapstructure:"check_interval"`
	MaxConcurrentRules int           `mapstructure:"max_concurrent_rules"`
	RuleTimeout        time.Duration `mapstructure:"rule_timeout"`
}

// MonitoringConfig holds monitoring configuration
type MonitoringConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	MetricsPath   string `mapstructure:"metrics_path"`
	HealthPath    string `mapstructure:"health_path"`
	ReadinessPath string `mapstructure:"readiness_path"`
	LivenessPath  string `mapstructure:"liveness_path"`
}

// KubernetesConfig holds Kubernetes configuration
type KubernetesConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	Namespace  string `mapstructure:"namespace"`
	ConfigPath string `mapstructure:"config_path"`
	InCluster  bool   `mapstructure:"in_cluster"`
}

// LoadConfig loads configuration from file and environment variables
func LoadConfig(configPath ...string) (*Config, error) {
	// Set default config path if not provided
	if len(configPath) > 0 && configPath[0] != "" {
		viper.SetConfigFile(configPath[0])
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/policy-engine")
	}
	viper.SetConfigType("yaml")

	// Set default values
	setDefaults()

	// Enable reading from environment variables
	viper.AutomaticEnv()

	// Read config file (ignore error if file doesn't exist)
	if err := viper.ReadInConfig(); err != nil {
		// If it's a file not found error, continue with defaults
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	setServerDefaults()
	setDatabaseDefaults()
	setRedisDefaults()
	setLoggingDefaults()
	setPolicyDefaults()
	setAutomationDefaults()
	setMonitoringDefaults()
	setKubernetesDefaults()
}

func setServerDefaults() {
	viper.SetDefault("server.port", 8005)
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "120s")
}

func setDatabaseDefaults() {
	viper.SetDefault("database.type", "postgres")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.database", "policy_engine")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_connections", 100)
	viper.SetDefault("database.connection_timeout", "30s")
}

func setRedisDefaults() {
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.database", 0)
	viper.SetDefault("redis.pool_size", 10)
	viper.SetDefault("redis.min_idle_conns", 5)
	viper.SetDefault("redis.dial_timeout", "5s")
	viper.SetDefault("redis.read_timeout", "3s")
	viper.SetDefault("redis.write_timeout", "3s")
}

func setLoggingDefaults() {
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
	viper.SetDefault("logging.output", "stdout")
}

func setPolicyDefaults() {
	viper.SetDefault("policy.cache_ttl", "300s")
	viper.SetDefault("policy.max_policies", 1000)
	viper.SetDefault("policy.evaluation_timeout", "10s")
	viper.SetDefault("policy.conflict_resolution", "priority")
}

func setAutomationDefaults() {
	viper.SetDefault("automation.enabled", true)
	viper.SetDefault("automation.check_interval", "30s")
	viper.SetDefault("automation.max_concurrent_rules", 10)
	viper.SetDefault("automation.rule_timeout", "60s")
}

func setMonitoringDefaults() {
	viper.SetDefault("monitoring.enabled", true)
	viper.SetDefault("monitoring.metrics_path", "/metrics")
	viper.SetDefault("monitoring.health_path", "/health")
	viper.SetDefault("monitoring.readiness_path", "/ready")
	viper.SetDefault("monitoring.liveness_path", "/live")
}

func setKubernetesDefaults() {
	viper.SetDefault("kubernetes.enabled", true)
	viper.SetDefault("kubernetes.namespace", "kcloud-system")
	viper.SetDefault("kubernetes.in_cluster", true)
}

// GetDSN returns database connection string
func (d *DatabaseConfig) GetDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.Username, d.Password, d.Database, d.SSLMode)
}

// GetRedisAddr returns Redis address
func (r *RedisConfig) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}
