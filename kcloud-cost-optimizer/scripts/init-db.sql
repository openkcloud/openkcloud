-- Policy Engine Database Initialization Script
-- This script creates the necessary database schema for the policy engine

-- Create database if not exists (handled by POSTGRES_DB environment variable)
-- Create extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "btree_gin";

-- Create policies table
CREATE TABLE IF NOT EXISTS policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL UNIQUE,
    type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'inactive',
    priority INTEGER NOT NULL DEFAULT 100,
    namespace VARCHAR(255),
    metadata JSONB,
    spec JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_by VARCHAR(255),
    version INTEGER DEFAULT 1
);

-- Create workloads table
CREATE TABLE IF NOT EXISTS workloads (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL,
    namespace VARCHAR(255),
    cluster_id VARCHAR(255),
    node_id VARCHAR(255),
    labels JSONB,
    annotations JSONB,
    requirements JSONB,
    metrics JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create decisions table
CREATE TABLE IF NOT EXISTS decisions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workload_id VARCHAR(255) NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    decision_type VARCHAR(50) NOT NULL,
    decision_reason VARCHAR(255),
    decision_message TEXT,
    action_type VARCHAR(50),
    action_parameters JSONB,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    executed_at TIMESTAMP WITH TIME ZONE,
    result JSONB
);

-- Create evaluations table
CREATE TABLE IF NOT EXISTS evaluations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workload_id VARCHAR(255) NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    evaluation_type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL,
    result VARCHAR(20),
    score DECIMAL(5,2),
    violations JSONB,
    recommendations JSONB,
    constraints JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_ms INTEGER
);

-- Create automation_rules table
CREATE TABLE IF NOT EXISTS automation_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL UNIQUE,
    type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'inactive',
    priority INTEGER NOT NULL DEFAULT 100,
    namespace VARCHAR(255),
    triggers JSONB,
    conditions JSONB,
    actions JSONB,
    execution JSONB,
    monitoring JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_by VARCHAR(255),
    version INTEGER DEFAULT 1
);

-- Create automation_rule_executions table
CREATE TABLE IF NOT EXISTS automation_rule_executions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    rule_id UUID NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
    trigger_type VARCHAR(50) NOT NULL,
    trigger_data JSONB,
    status VARCHAR(20) NOT NULL,
    result JSONB,
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_ms INTEGER
);

-- Create events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    event_type VARCHAR(50) NOT NULL,
    event_source VARCHAR(255) NOT NULL,
    event_data JSONB,
    workload_id VARCHAR(255) REFERENCES workloads(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    rule_id UUID REFERENCES automation_rules(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    processed_at TIMESTAMP WITH TIME ZONE,
    processed BOOLEAN DEFAULT FALSE
);

-- Create metrics table
CREATE TABLE IF NOT EXISTS metrics (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    metric_name VARCHAR(255) NOT NULL,
    metric_type VARCHAR(50) NOT NULL,
    metric_value DECIMAL(15,6) NOT NULL,
    metric_unit VARCHAR(20),
    labels JSONB,
    workload_id VARCHAR(255) REFERENCES workloads(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES policies(id) ON DELETE SET NULL,
    rule_id UUID REFERENCES automation_rules(id) ON DELETE SET NULL,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for better performance
CREATE INDEX IF NOT EXISTS idx_policies_name ON policies(name);
CREATE INDEX IF NOT EXISTS idx_policies_type ON policies(type);
CREATE INDEX IF NOT EXISTS idx_policies_status ON policies(status);
CREATE INDEX IF NOT EXISTS idx_policies_namespace ON policies(namespace);
CREATE INDEX IF NOT EXISTS idx_policies_priority ON policies(priority);
CREATE INDEX IF NOT EXISTS idx_policies_created_at ON policies(created_at);
CREATE INDEX IF NOT EXISTS idx_policies_metadata ON policies USING GIN(metadata);
CREATE INDEX IF NOT EXISTS idx_policies_spec ON policies USING GIN(spec);

CREATE INDEX IF NOT EXISTS idx_workloads_name ON workloads(name);
CREATE INDEX IF NOT EXISTS idx_workloads_type ON workloads(type);
CREATE INDEX IF NOT EXISTS idx_workloads_status ON workloads(status);
CREATE INDEX IF NOT EXISTS idx_workloads_namespace ON workloads(namespace);
CREATE INDEX IF NOT EXISTS idx_workloads_cluster_id ON workloads(cluster_id);
CREATE INDEX IF NOT EXISTS idx_workloads_node_id ON workloads(node_id);
CREATE INDEX IF NOT EXISTS idx_workloads_labels ON workloads USING GIN(labels);
CREATE INDEX IF NOT EXISTS idx_workloads_annotations ON workloads USING GIN(annotations);
CREATE INDEX IF NOT EXISTS idx_workloads_created_at ON workloads(created_at);

CREATE INDEX IF NOT EXISTS idx_decisions_workload_id ON decisions(workload_id);
CREATE INDEX IF NOT EXISTS idx_decisions_policy_id ON decisions(policy_id);
CREATE INDEX IF NOT EXISTS idx_decisions_decision_type ON decisions(decision_type);
CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status);
CREATE INDEX IF NOT EXISTS idx_decisions_created_at ON decisions(created_at);

CREATE INDEX IF NOT EXISTS idx_evaluations_workload_id ON evaluations(workload_id);
CREATE INDEX IF NOT EXISTS idx_evaluations_policy_id ON evaluations(policy_id);
CREATE INDEX IF NOT EXISTS idx_evaluations_status ON evaluations(status);
CREATE INDEX IF NOT EXISTS idx_evaluations_result ON evaluations(result);
CREATE INDEX IF NOT EXISTS idx_evaluations_created_at ON evaluations(created_at);

CREATE INDEX IF NOT EXISTS idx_automation_rules_name ON automation_rules(name);
CREATE INDEX IF NOT EXISTS idx_automation_rules_type ON automation_rules(type);
CREATE INDEX IF NOT EXISTS idx_automation_rules_status ON automation_rules(status);
CREATE INDEX IF NOT EXISTS idx_automation_rules_namespace ON automation_rules(namespace);
CREATE INDEX IF NOT EXISTS idx_automation_rules_priority ON automation_rules(priority);
CREATE INDEX IF NOT EXISTS idx_automation_rules_triggers ON automation_rules USING GIN(triggers);
CREATE INDEX IF NOT EXISTS idx_automation_rules_conditions ON automation_rules USING GIN(conditions);
CREATE INDEX IF NOT EXISTS idx_automation_rules_actions ON automation_rules USING GIN(actions);

CREATE INDEX IF NOT EXISTS idx_automation_rule_executions_rule_id ON automation_rule_executions(rule_id);
CREATE INDEX IF NOT EXISTS idx_automation_rule_executions_trigger_type ON automation_rule_executions(trigger_type);
CREATE INDEX IF NOT EXISTS idx_automation_rule_executions_status ON automation_rule_executions(status);
CREATE INDEX IF NOT EXISTS idx_automation_rule_executions_started_at ON automation_rule_executions(started_at);

CREATE INDEX IF NOT EXISTS idx_events_event_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_event_source ON events(event_source);
CREATE INDEX IF NOT EXISTS idx_events_workload_id ON events(workload_id);
CREATE INDEX IF NOT EXISTS idx_events_policy_id ON events(policy_id);
CREATE INDEX IF NOT EXISTS idx_events_rule_id ON events(rule_id);
CREATE INDEX IF NOT EXISTS idx_events_processed ON events(processed);
CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at);
CREATE INDEX IF NOT EXISTS idx_events_event_data ON events USING GIN(event_data);

CREATE INDEX IF NOT EXISTS idx_metrics_metric_name ON metrics(metric_name);
CREATE INDEX IF NOT EXISTS idx_metrics_metric_type ON metrics(metric_type);
CREATE INDEX IF NOT EXISTS idx_metrics_workload_id ON metrics(workload_id);
CREATE INDEX IF NOT EXISTS idx_metrics_policy_id ON metrics(policy_id);
CREATE INDEX IF NOT EXISTS idx_metrics_rule_id ON metrics(rule_id);
CREATE INDEX IF NOT EXISTS idx_metrics_timestamp ON metrics(timestamp);
CREATE INDEX IF NOT EXISTS idx_metrics_labels ON metrics USING GIN(labels);

-- Create triggers for updated_at timestamps
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_policies_updated_at BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_workloads_updated_at BEFORE UPDATE ON workloads
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_decisions_updated_at BEFORE UPDATE ON decisions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_evaluations_updated_at BEFORE UPDATE ON evaluations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_automation_rules_updated_at BEFORE UPDATE ON automation_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create views for common queries
CREATE OR REPLACE VIEW policy_summary AS
SELECT 
    p.id,
    p.name,
    p.type,
    p.status,
    p.priority,
    p.namespace,
    p.created_at,
    p.updated_at,
    COUNT(d.id) as decision_count,
    COUNT(e.id) as evaluation_count
FROM policies p
LEFT JOIN decisions d ON p.id = d.policy_id
LEFT JOIN evaluations e ON p.id = e.policy_id
GROUP BY p.id, p.name, p.type, p.status, p.priority, p.namespace, p.created_at, p.updated_at;

CREATE OR REPLACE VIEW workload_summary AS
SELECT 
    w.id,
    w.name,
    w.type,
    w.status,
    w.namespace,
    w.cluster_id,
    w.node_id,
    w.created_at,
    w.updated_at,
    COUNT(d.id) as decision_count,
    COUNT(e.id) as evaluation_count,
    COUNT(m.id) as metric_count
FROM workloads w
LEFT JOIN decisions d ON w.id = d.workload_id
LEFT JOIN evaluations e ON w.id = e.workload_id
LEFT JOIN metrics m ON w.id = m.workload_id
GROUP BY w.id, w.name, w.type, w.status, w.namespace, w.cluster_id, w.node_id, w.created_at, w.updated_at;

-- Insert sample data for testing
INSERT INTO policies (name, type, status, priority, namespace, spec, created_by) VALUES
('cost-optimization-default', 'cost-optimization', 'active', 100, 'default', '{"objectives": [{"type": "cost-reduction", "weight": 0.4, "target": "20%"}]}', 'system'),
('workload-priority-default', 'workload-priority', 'active', 150, 'default', '{"priorityLevels": [{"level": "critical", "priority": 1000}]}', 'system'),
('security-default', 'security', 'active', 400, 'default', '{"securityRules": [{"name": "non-root-user", "condition": "workload.securityContext.runAsUser != 0"}]}', 'system')
ON CONFLICT (name) DO NOTHING;

-- Grant permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO policy_user;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO policy_user;
GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO policy_user;
