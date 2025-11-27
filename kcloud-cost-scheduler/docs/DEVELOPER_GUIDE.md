# KCloud Workload Optimizer Operator Developer Guide

This guide provides comprehensive information for developers working on the KCloud Workload Optimizer Operator.

## Table of Contents

- [Development Environment Setup](#development-environment-setup)
- [Project Structure](#project-structure)
- [Code Generation](#code-generation)
- [Building and Testing](#building-and-testing)
- [Contributing](#contributing)
- [Architecture Overview](#architecture-overview)
- [API Development](#api-development)
- [Controller Development](#controller-development)
- [Webhook Development](#webhook-development)
- [Testing Guidelines](#testing-guidelines)
- [Debugging](#debugging)

## Development Environment Setup

### Prerequisites

- Go 1.21 or later
- Kubernetes 1.19 or later
- Docker (for building images)
- Helm 3.0 or later (for chart development)
- kubectl
- controller-runtime
- kubebuilder

### Local Development Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/KETI-Cloud-Platform/k8s-workload-operator.git
   cd k8s-workload-operator
   ```

2. **Install dependencies**
   ```bash
   make deps
   ```

3. **Generate code**
   ```bash
   make generate
   ```

4. **Build the operator**
   ```bash
   make build
   ```

5. **Run locally**
   ```bash
   make run
   ```

### IDE Setup

#### VS Code

Install the following extensions:
- Go (golang.go)
- Kubernetes (ms-kubernetes-tools.vscode-kubernetes-tools)
- YAML (redhat.vscode-yaml)

#### GoLand/IntelliJ

1. Install the Kubernetes plugin
2. Configure Go modules
3. Set up Kubernetes integration

## Project Structure

```
k8s-workload-operator/
├── api/                          # API definitions
│   └── v1alpha1/                 # API version
│       ├── groupversion_info.go   # API group version info
│       ├── workloadoptimizer_types.go
│       ├── costpolicy_types.go
│       └── powerpolicy_types.go
├── cmd/                          # Main applications
│   └── main.go                   # Operator entry point
├── internal/                     # Private application code
│   └── controller/               # Controllers
│       ├── workloadoptimizer_controller.go
│       └── workloadoptimizer_controller_test.go
├── pkg/                          # Public library code
│   ├── optimizer/                # Optimization engine
│   │   ├── engine.go
│   │   ├── cost_calculator.go
│   │   ├── power_calculator.go
│   │   ├── policy_applier.go
│   │   └── optimization_strategies.go
│   ├── scheduler/                 # Scheduling logic
│   │   ├── scheduler.go
│   │   ├── advanced_scheduler.go
│   │   ├── policy_manager.go
│   │   └── metrics_collector.go
│   ├── webhook/                  # Admission webhooks
│   │   ├── pod_mutator.go
│   │   ├── workloadoptimizer_validator.go
│   │   └── webhook_config.go
│   └── metrics/                  # Metrics collection
│       ├── metrics.go
│       └── collector.go
├── config/                       # Configuration files
│   ├── crd/                     # CRD manifests
│   ├── rbac/                    # RBAC manifests
│   ├── prometheus/              # Monitoring manifests
│   └── samples/                 # Sample resources
├── charts/                       # Helm charts
│   └── kcloud-operator/
├── test/                         # Test files
│   ├── e2e/                     # End-to-end tests
│   ├── integration/             # Integration tests
│   └── utils/                   # Test utilities
├── docs/                         # Documentation
├── scripts/                      # Build and deployment scripts
├── Makefile                      # Build automation
├── go.mod                        # Go module definition
├── go.sum                        # Go module checksums
└── README.md                     # Project documentation
```

## Code Generation

The project uses several code generation tools:

### Controller Runtime

Generate controller code:
```bash
make generate
```

This generates:
- Deep copy methods for CRDs
- Client code for CRDs
- Controller boilerplate

### CRD Generation

Generate CRD manifests:
```bash
make manifests
```

This generates:
- CRD YAML files
- RBAC manifests
- Webhook configurations

### All Generation

Generate everything:
```bash
make all
```

## Building and Testing

### Building

```bash
# Build the operator binary
make build

# Build Docker image
make docker-build

# Build and push image
make docker-push
```

### Testing

```bash
# Run unit tests
make test

# Run specific test packages
make test-controller
make test-optimizer
make test-scheduler

# Run integration tests
make test-integration

# Run E2E tests
make test-e2e

# Generate test coverage
make test-coverage
```

### Linting

```bash
# Run linters
make lint

# Format code
make fmt

# Verify code
make verify
```

## Contributing

### Development Workflow

1. **Create a feature branch**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes**
   - Follow Go coding standards
   - Add tests for new functionality
   - Update documentation

3. **Run tests**
   ```bash
   make test
   make test-integration
   ```

4. **Commit your changes**
   ```bash
   git add .
   git commit -m "feat: add your feature"
   ```

5. **Push and create PR**
   ```bash
   git push origin feature/your-feature-name
   ```

### Code Standards

- Follow Go best practices
- Use meaningful variable names
- Add comments for public functions
- Write comprehensive tests
- Update documentation

### Commit Message Format

Use conventional commits:
- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `test:` for test changes
- `refactor:` for code refactoring
- `chore:` for maintenance tasks

## Architecture Overview

### High-Level Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Kubernetes    │    │   KCloud        │    │   External      │
│   API Server    │◄──►│   Operator      │◄──►│   Services      │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌─────────────────┐
                       │   Optimization  │
                       │   Engine        │
                       └─────────────────┘
```

### Component Interaction

```
WorkloadOptimizer CRD
         │
         ▼
┌─────────────────┐
│   Controller    │
└─────────────────┘
         │
         ▼
┌─────────────────┐    ┌─────────────────┐
│   Optimizer     │◄──►│   Scheduler     │
│   Engine        │    │                 │
└─────────────────┘    └─────────────────┘
         │                       │
         ▼                       ▼
┌─────────────────┐    ┌─────────────────┐
│   Cost          │    │   Node          │
│   Calculator    │    │   Selection     │
└─────────────────┘    └─────────────────┘
```

## API Development

### Adding New CRDs

1. **Define the API types**
   ```go
   // api/v1alpha1/yourresource_types.go
   type YourResource struct {
       metav1.TypeMeta   `json:",inline"`
       metav1.ObjectMeta `json:"metadata,omitempty"`
       
       Spec   YourResourceSpec   `json:"spec,omitempty"`
       Status YourResourceStatus `json:"status,omitempty"`
   }
   ```

2. **Add kubebuilder markers**
   ```go
   // +kubebuilder:object:root=true
   // +kubebuilder:subresource:status
   // +kubebuilder:resource:scope=Namespaced
   ```

3. **Generate code**
   ```bash
   make generate
   make manifests
   ```

### API Validation

Use kubebuilder validation markers:

```go
type YourResourceSpec struct {
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=10
    Priority int32 `json:"priority"`
    
    // +kubebuilder:validation:Enum=training;inference;batch;streaming
    WorkloadType string `json:"workloadType"`
    
    // +kubebuilder:validation:Pattern=^[0-9]+(m|Gi?)$
    CPU string `json:"cpu"`
}
```

## Controller Development

### Controller Structure

```go
type YourResourceReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Log    logr.Logger
}

func (r *YourResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Get the resource
    var resource YourResource
    if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. Process the resource
    result, err := r.processResource(ctx, &resource)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. Update status
    if err := r.updateStatus(ctx, &resource, result); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}
```

### Error Handling

```go
func (r *YourResourceReconciler) processResource(ctx context.Context, resource *YourResource) (*Result, error) {
    // Handle different error types
    if err := r.validateResource(resource); err != nil {
        return nil, fmt.Errorf("validation failed: %w", err)
    }
    
    // Handle temporary errors
    if err := r.externalCall(ctx); err != nil {
        if isTemporaryError(err) {
            return nil, fmt.Errorf("temporary error, will retry: %w", err)
        }
        return nil, fmt.Errorf("permanent error: %w", err)
    }
    
    return &Result{}, nil
}
```

### Status Updates

```go
func (r *YourResourceReconciler) updateStatus(ctx context.Context, resource *YourResource, result *Result) error {
    // Update status fields
    resource.Status.Phase = result.Phase
    resource.Status.LastUpdated = metav1.Now()
    
    // Add conditions
    condition := metav1.Condition{
        Type:               "Ready",
        Status:             metav1.ConditionTrue,
        Reason:             "Reconciled",
        Message:            "Resource successfully reconciled",
        LastTransitionTime: metav1.Now(),
    }
    
    meta.SetStatusCondition(&resource.Status.Conditions, condition)
    
    // Update the resource
    return r.Status().Update(ctx, resource)
}
```

## Webhook Development

### Mutating Webhook

```go
func (w *YourMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
    // Decode the object
    obj := &YourResource{}
    if err := w.Decoder.Decode(req, obj); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }
    
    // Apply mutations
    mutated := w.applyMutations(obj)
    
    // Create patch
    patch, err := w.createPatch(obj, mutated)
    if err != nil {
        return admission.Errored(http.StatusInternalServerError, err)
    }
    
    return admission.PatchResponseFromRaw(req.Object.Raw, patch)
}
```

### Validating Webhook

```go
func (w *YourValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
    // Decode the object
    obj := &YourResource{}
    if err := w.Decoder.Decode(req, obj); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }
    
    // Validate the object
    if err := w.validateObject(obj); err != nil {
        return admission.Denied(err.Error())
    }
    
    return admission.Allowed("")
}
```

## Testing Guidelines

### Unit Tests

```go
func TestYourFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    Input
        expected Output
        wantErr  bool
    }{
        {
            name: "valid input",
            input: Input{Value: "test"},
            expected: Output{Result: "test"},
            wantErr: false,
        },
        {
            name: "invalid input",
            input: Input{Value: ""},
            expected: Output{},
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := YourFunction(tt.input)
            
            if tt.wantErr {
                assert.Error(t, err)
                return
            }
            
            assert.NoError(t, err)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

### Controller Tests

```go
func TestYourResourceReconciler(t *testing.T) {
    // Setup test environment
    env := &envtest.Environment{
        CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
    }
    
    cfg, err := env.Start()
    require.NoError(t, err)
    defer env.Stop()
    
    // Create client
    c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
    require.NoError(t, err)
    
    // Create reconciler
    r := &YourResourceReconciler{
        Client: c,
        Scheme: scheme.Scheme,
        Log:    logr.Discard(),
    }
    
    // Test reconciliation
    req := ctrl.Request{
        NamespacedName: types.NamespacedName{
            Name:      "test-resource",
            Namespace: "default",
        },
    }
    
    result, err := r.Reconcile(context.Background(), req)
    assert.NoError(t, err)
    assert.Equal(t, ctrl.Result{}, result)
}
```

### Integration Tests

```go
func TestIntegration(t *testing.T) {
    // Setup test cluster
    cluster := &envtest.Environment{
        CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
    }
    
    cfg, err := cluster.Start()
    require.NoError(t, err)
    defer cluster.Stop()
    
    // Create test resources
    testResource := &YourResource{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test",
            Namespace: "default",
        },
        Spec: YourResourceSpec{
            // Test spec
        },
    }
    
    // Apply resource
    c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
    require.NoError(t, err)
    
    err = c.Create(context.Background(), testResource)
    require.NoError(t, err)
    
    // Wait for reconciliation
    eventually(t, func() bool {
        var resource YourResource
        err := c.Get(context.Background(), types.NamespacedName{
            Name:      "test",
            Namespace: "default",
        }, &resource)
        return err == nil && resource.Status.Phase == "Ready"
    }, 30*time.Second, time.Second)
}
```

## Debugging

### Local Debugging

1. **Run with debug logging**
   ```bash
   make run ARGS="--log-level=debug"
   ```

2. **Use Delve debugger**
   ```bash
   dlv debug ./cmd/main.go
   ```

3. **Add debug prints**
   ```go
   log.V(1).Info("Debug message", "key", "value")
   ```

### Remote Debugging

1. **Port forward to operator**
   ```bash
   kubectl port-forward -n kcloud-operator-system svc/kcloud-operator 8080:8080
   ```

2. **Check metrics**
   ```bash
   curl http://localhost:8080/metrics
   ```

3. **Check logs**
   ```bash
   kubectl logs -n kcloud-operator-system deployment/kcloud-operator -f
   ```

### Common Debugging Scenarios

1. **Controller not reconciling**
   - Check RBAC permissions
   - Verify CRD installation
   - Check controller logs

2. **Webhook not working**
   - Verify webhook configuration
   - Check certificate validity
   - Test webhook endpoint

3. **Optimization not working**
   - Check optimizer engine logs
   - Verify resource constraints
   - Check node availability

### Performance Debugging

1. **Profile CPU usage**
   ```bash
   go tool pprof http://localhost:8080/debug/pprof/profile
   ```

2. **Profile memory usage**
   ```bash
   go tool pprof http://localhost:8080/debug/pprof/heap
   ```

3. **Trace execution**
   ```bash
   go tool trace trace.out
   ```

## Best Practices

### Code Organization

- Keep controllers focused on a single responsibility
- Use interfaces for external dependencies
- Implement proper error handling
- Add comprehensive logging

### Performance

- Use informers for efficient resource watching
- Implement proper caching
- Avoid blocking operations in reconciliation
- Use context for cancellation

### Security

- Validate all inputs
- Use RBAC for authorization
- Implement proper webhook security
- Follow Kubernetes security best practices

### Testing

- Write tests for all public functions
- Use table-driven tests
- Mock external dependencies
- Test error conditions

### Documentation

- Document public APIs
- Add examples for complex features
- Keep README up to date
- Document configuration options
