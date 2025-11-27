package routes

import (
	"fmt"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/kcloud-opt/policy/api/handlers"
	"github.com/kcloud-opt/policy/internal/config"
	"github.com/kcloud-opt/policy/internal/logger"
)

// Router sets up all the routes for the policy engine API
type Router struct {
	handlers *handlers.Handlers
	config   *config.Config
	logger   *logger.Logger
}

// NewRouter creates a new router instance
func NewRouter(handlers *handlers.Handlers, config *config.Config, logger *logger.Logger) *Router {
	return &Router{
		handlers: handlers,
		config:   config,
		logger:   logger,
	}
}

func (r *Router) SetupRoutes() *gin.Engine {
	if r.config.Server.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	r.setupMiddleware(router)
	r.setupHealthRoutes(router)
	r.setupAPIRoutes(router)

	return router
}

func (r *Router) setupMiddleware(router *gin.Engine) {
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC1123),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))

	router.Use(gin.Recovery())

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.Use(func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Header("X-Request-ID", requestID)
		c.Set("request_id", requestID)
		c.Next()
	})

	router.Use(func(c *gin.Context) {
		time.Sleep(10 * time.Millisecond)
		c.Next()
	})
}

func (r *Router) setupHealthRoutes(router *gin.Engine) {
	router.GET("/health", r.handlers.Health.Health)
	router.GET("/ready", r.handlers.Health.Readiness)
	router.GET("/live", r.handlers.Health.Liveness)
	router.GET("/status", r.handlers.Health.SystemStatus)
	router.GET("/metrics", r.handlers.Health.Metrics)
	router.GET("/info", r.handlers.Health.Info)
}

func (r *Router) setupAPIRoutes(router *gin.Engine) {
	v1 := router.Group("/api/v1")
	{
		policies := v1.Group("/policies")
		{
			policies.GET("", r.handlers.Policy.ListPolicies)
			policies.POST("", r.handlers.Policy.CreatePolicy)
			policies.GET("/search", r.handlers.Policy.SearchPolicies)
			policies.GET("/:id", r.handlers.Policy.GetPolicy)
			policies.PUT("/:id", r.handlers.Policy.UpdatePolicy)
			policies.DELETE("/:id", r.handlers.Policy.DeletePolicy)
			policies.POST("/:id/enable", r.handlers.Policy.EnablePolicy)
			policies.POST("/:id/disable", r.handlers.Policy.DisablePolicy)
			policies.GET("/:id/versions", r.handlers.Policy.GetPolicyVersions)
		}

		workloads := v1.Group("/workloads")
		{
			workloads.GET("", r.handlers.Workload.ListWorkloads)
			workloads.POST("", r.handlers.Workload.CreateWorkload)
			workloads.GET("/search", r.handlers.Workload.SearchWorkloads)
			workloads.GET("/:id", r.handlers.Workload.GetWorkload)
			workloads.PUT("/:id", r.handlers.Workload.UpdateWorkload)
			workloads.DELETE("/:id", r.handlers.Workload.DeleteWorkload)
			workloads.GET("/:id/metrics", r.handlers.Workload.GetWorkloadMetrics)
			workloads.GET("/:id/history", r.handlers.Workload.GetWorkloadHistory)
		}

		evaluations := v1.Group("/evaluations")
		{
			evaluations.GET("", r.handlers.Evaluation.ListEvaluations)
			evaluations.POST("", r.handlers.Evaluation.EvaluateWorkload)
			evaluations.POST("/bulk", r.handlers.Evaluation.BulkEvaluateWorkloads)
			evaluations.GET("/history", r.handlers.Evaluation.GetEvaluationHistory)
			evaluations.GET("/statistics", r.handlers.Evaluation.GetEvaluationStatistics)
			evaluations.GET("/health", r.handlers.Evaluation.GetEvaluationHealth)
			evaluations.GET("/:id", r.handlers.Evaluation.GetEvaluation)
		}

		automation := v1.Group("/automation")
		{
			rules := automation.Group("/rules")
			{
				rules.GET("", r.handlers.Automation.ListAutomationRules)
				rules.POST("", r.handlers.Automation.CreateAutomationRule)
				rules.GET("/:id", r.handlers.Automation.GetAutomationRule)
				rules.PUT("/:id", r.handlers.Automation.UpdateAutomationRule)
				rules.DELETE("/:id", r.handlers.Automation.DeleteAutomationRule)
				rules.POST("/:id/enable", r.handlers.Automation.EnableAutomationRule)
				rules.POST("/:id/disable", r.handlers.Automation.DisableAutomationRule)
				rules.POST("/:id/execute", r.handlers.Automation.ExecuteAutomationRule)
				rules.GET("/:id/history", r.handlers.Automation.GetAutomationRuleHistory)
			}

			automation.GET("/statistics", r.handlers.Automation.GetAutomationStatistics)
			automation.GET("/health", r.handlers.Automation.GetAutomationHealth)
		}
	}
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
