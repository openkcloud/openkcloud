package validator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kcloud-opt/policy/internal/types"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

// SchemaValidator provides JSON schema validation functionality
type SchemaValidator struct {
	logger  types.Logger
	schemas map[string]*gojsonschema.Schema
}

// NewSchemaValidator creates a new schema validator instance
func NewSchemaValidator(logger types.Logger) *SchemaValidator {
	return &SchemaValidator{
		logger:  logger,
		schemas: make(map[string]*gojsonschema.Schema),
	}
}

// LoadSchemas loads JSON schemas for validation
func (sv *SchemaValidator) LoadSchemas() error {
	// Load policy schema
	policySchema := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"required": ["metadata", "spec"],
		"properties": {
			"metadata": {
				"type": "object",
				"required": ["name", "type"],
				"properties": {
					"name": {
						"type": "string",
						"pattern": "^[a-z0-9]([a-z0-9\\-]*[a-z0-9])?$",
						"maxLength": 253
					},
					"type": {
						"type": "string",
						"enum": ["cost-optimization", "automation", "workload-priority", "security", "resource-quota"]
					},
					"status": {
						"type": "string",
						"enum": ["active", "inactive", "draft"]
					},
					"priority": {
						"type": "integer",
						"minimum": 1,
						"maximum": 1000
					},
					"namespace": {
						"type": "string",
						"pattern": "^[a-z0-9]([a-z0-9\\-]*[a-z0-9])?$",
						"maxLength": 63
					},
					"labels": {
						"type": "object",
						"additionalProperties": {
							"type": "string",
							"maxLength": 63
						}
					},
					"annotations": {
						"type": "object",
						"additionalProperties": {
							"type": "string",
							"maxLength": 262144
						}
					}
				}
			},
			"spec": {
				"type": "object",
				"required": ["type"],
				"properties": {
					"type": {
						"type": "string",
						"enum": ["cost-optimization", "automation", "workload-priority", "security", "resource-quota"]
					},
					"target": {
						"type": "object",
						"properties": {
							"namespaces": {
								"type": "array",
								"items": {
									"type": "string"
								}
							},
							"workloadTypes": {
								"type": "array",
								"items": {
									"type": "string"
								}
							},
							"labelSelectors": {
								"type": "object"
							}
						}
					},
					"objectives": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["type", "weight", "target"],
							"properties": {
								"type": {
									"type": "string"
								},
								"weight": {
									"type": "number",
									"minimum": 0,
									"maximum": 1
								},
								"target": {
									"type": "string"
								}
							}
						}
					},
					"constraints": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["type", "value"],
							"properties": {
								"type": {
									"type": "string"
								},
								"value": {
									"type": "string"
								},
								"description": {
									"type": "string"
								}
							}
						}
					},
					"rules": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["name", "condition", "action"],
							"properties": {
								"name": {
									"type": "string"
								},
								"condition": {
									"type": "string"
								},
								"action": {
									"type": "string"
								},
								"parameters": {
									"type": "object"
								}
							}
						}
					},
					"actions": {
						"type": "array",
						"items": {
							"type": "object",
							"required": ["type"],
							"properties": {
								"type": {
									"type": "string"
								},
								"parameters": {
									"type": "object"
								}
							}
						}
					}
				}
			}
		}
	}`

	schema, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(policySchema))
	if err != nil {
		return fmt.Errorf("failed to load policy schema: %w", err)
	}
	sv.schemas["policy"] = schema

	// Load workload schema
	workloadSchema := `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"required": ["id", "name", "type", "status"],
		"properties": {
			"id": {
				"type": "string",
				"minLength": 1
			},
			"name": {
				"type": "string",
				"minLength": 1
			},
			"type": {
				"type": "string",
				"enum": ["deployment", "statefulset", "daemonset", "job", "cronjob"]
			},
			"status": {
				"type": "string",
				"enum": ["running", "stopped", "pending", "failed"]
			},
			"namespace": {
				"type": "string"
			},
			"cluster_id": {
				"type": "string"
			},
			"node_id": {
				"type": "string"
			},
			"labels": {
				"type": "object",
				"additionalProperties": {
					"type": "string"
				}
			},
			"annotations": {
				"type": "object",
				"additionalProperties": {
					"type": "string"
				}
			},
			"requirements": {
				"type": "object",
				"properties": {
					"cpu": {
						"type": "string"
					},
					"memory": {
						"type": "string"
					},
					"storage": {
						"type": "string"
					}
				}
			}
		}
	}`

	schema, err = gojsonschema.NewSchema(gojsonschema.NewStringLoader(workloadSchema))
	if err != nil {
		return fmt.Errorf("failed to load workload schema: %w", err)
	}
	sv.schemas["workload"] = schema

	return nil
}

// ValidatePolicy validates a policy against JSON schema
func (sv *SchemaValidator) ValidatePolicy(policy *types.Policy) error {
	if policy == nil {
		return fmt.Errorf("policy cannot be nil")
	}

	schema, exists := sv.schemas["policy"]
	if !exists {
		return fmt.Errorf("policy schema not loaded")
	}

	// Convert policy to JSON
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("failed to marshal policy to JSON: %w", err)
	}

	// Validate against schema
	result, err := schema.Validate(gojsonschema.NewBytesLoader(policyJSON))
	if err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	if !result.Valid() {
		var errors []string
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
		return fmt.Errorf("policy validation failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ValidateWorkload validates a workload against JSON schema
func (sv *SchemaValidator) ValidateWorkload(workload *types.Workload) error {
	if workload == nil {
		return fmt.Errorf("workload cannot be nil")
	}

	schema, exists := sv.schemas["workload"]
	if !exists {
		return fmt.Errorf("workload schema not loaded")
	}

	// Convert workload to JSON
	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		return fmt.Errorf("failed to marshal workload to JSON: %w", err)
	}

	// Validate against schema
	result, err := schema.Validate(gojsonschema.NewBytesLoader(workloadJSON))
	if err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	if !result.Valid() {
		var errors []string
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
		return fmt.Errorf("workload validation failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ValidateJSON validates JSON data against a schema
func (sv *SchemaValidator) ValidateJSON(data []byte, schemaName string) error {
	schema, exists := sv.schemas[schemaName]
	if !exists {
		return fmt.Errorf("schema %s not loaded", schemaName)
	}

	result, err := schema.Validate(gojsonschema.NewBytesLoader(data))
	if err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	if !result.Valid() {
		var errors []string
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
		return fmt.Errorf("JSON validation failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ValidateYAML validates YAML data against a schema
func (sv *SchemaValidator) ValidateYAML(data []byte, schemaName string) error {
	// Convert YAML to JSON first
	var jsonData interface{}
	if err := yaml.Unmarshal(data, &jsonData); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		return fmt.Errorf("failed to convert YAML to JSON: %w", err)
	}

	return sv.ValidateJSON(jsonBytes, schemaName)
}

// GetSchema returns a loaded schema by name
func (sv *SchemaValidator) GetSchema(name string) (*gojsonschema.Schema, error) {
	schema, exists := sv.schemas[name]
	if !exists {
		return nil, fmt.Errorf("schema %s not found", name)
	}
	return schema, nil
}

// ListSchemas returns a list of loaded schema names
func (sv *SchemaValidator) ListSchemas() []string {
	var names []string
	for name := range sv.schemas {
		names = append(names, name)
	}
	return names
}
