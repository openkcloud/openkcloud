-- P2 Catalog bootstrap schema (SQLite). Adapter-replaceable contract for Postgres later.

CREATE TABLE IF NOT EXISTS hardware_profile (
  accelerator_uuid TEXT PRIMARY KEY,
  vendor TEXT,
  model TEXT,
  architecture TEXT,
  power_limit_w REAL,
  vram_mb INTEGER,
  partition_mode TEXT,
  notes TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS reference_profile (
  profile_key TEXT PRIMARY KEY,
  model TEXT NOT NULL,
  runtime TEXT,
  accelerator TEXT,
  precision TEXT,
  input_tokens INTEGER,
  output_tokens INTEGER,
  context_length INTEGER,
  batch INTEGER,
  concurrency INTEGER,
  predicted_p99_ms REAL,
  predicted_duration_s REAL,
  predicted_net_energy_j REAL,
  -- Device net energy proxy (historical). Server total for Q3 cost ranking is
  -- predicted_server_energy_j when present; do not assume net==server.
  predicted_server_energy_j REAL,
  source TEXT NOT NULL,
  confidence TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workload_run (
  run_uuid TEXT PRIMARY KEY,
  experiment_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  trace_id TEXT NOT NULL,
  workload_id TEXT NOT NULL,
  cohort TEXT,
  concurrency INTEGER,
  repeat_idx INTEGER,
  allocation_mode TEXT,
  runtime TEXT,
  model TEXT,
  source TEXT NOT NULL,
  quality_flag TEXT,
  decision_reason TEXT,
  routing_policy TEXT,
  decision_branch TEXT,
  npu_cap INTEGER,
  submitted_at TEXT,
  scheduled_at TEXT,
  container_started_at TEXT,
  endpoint_ready_at TEXT,
  load_started_at TEXT,
  load_ended_at TEXT,
  duration_s REAL,
  latency_p99_ms REAL,
  net_energy_j REAL,
  server_energy_j REAL,
  energy_per_success_j REAL,
  sla_pass INTEGER,
  requests_total INTEGER,
  requests_success INTEGER,
  error_rate REAL,
  git_sha TEXT,
  result_path TEXT,
  environment_key TEXT,
  UNIQUE(experiment_id, run_id)
);

CREATE TABLE IF NOT EXISTS request_sample (
  request_id TEXT PRIMARY KEY,
  run_uuid TEXT NOT NULL,
  trace_id TEXT NOT NULL,
  request_index INTEGER,
  endpoint TEXT,
  route_device TEXT,
  ttft_ms REAL,
  tpot_ms REAL,
  total_ms REAL,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  error TEXT,
  FOREIGN KEY(run_uuid) REFERENCES workload_run(run_uuid)
);

CREATE TABLE IF NOT EXISTS accelerator_sample (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_uuid TEXT NOT NULL,
  sample_timestamp REAL NOT NULL,
  accelerator_uuid TEXT,
  source TEXT NOT NULL,
  metric TEXT NOT NULL,
  value REAL NOT NULL,
  FOREIGN KEY(run_uuid) REFERENCES workload_run(run_uuid)
);

CREATE TABLE IF NOT EXISTS interference_context (
  run_uuid TEXT PRIMARY KEY,
  colocated_json TEXT,
  cnn_images_delta REAL,
  cnn_forwards_delta REAL,
  cnn_throughput_ips REAL,
  gpu_util_avg_pct REAL,
  npu_util_avg_pct REAL,
  FOREIGN KEY(run_uuid) REFERENCES workload_run(run_uuid)
);

CREATE TABLE IF NOT EXISTS placement_decision (
  decision_id TEXT PRIMARY KEY,
  trace_id TEXT NOT NULL,
  run_uuid TEXT,
  mode TEXT NOT NULL,
  decision_reason TEXT,
  routing_policy TEXT,
  decision_branch TEXT,
  evaluated_at TEXT NOT NULL,
  candidates_json TEXT,
  selected_shadow TEXT,
  selected_enforced TEXT,
  selected_actual TEXT,
  predicted_p99_ms REAL,
  predicted_duration_s REAL,
  predicted_net_energy_j REAL,
  predicted_server_energy_j REAL,
  confidence TEXT,
  score_components_json TEXT,
  regret_json TEXT,
  model_version TEXT
);

CREATE TABLE IF NOT EXISTS model_registry (
  version TEXT PRIMARY KEY,
  features_json TEXT,
  train_window TEXT,
  metrics_json TEXT,
  status TEXT,
  created_at TEXT NOT NULL
);

-- Adopted operational caps (NOT measured p99). Separate from reference_profile
-- so policy values cannot overwrite exploratory MEASURED predictions.
CREATE TABLE IF NOT EXISTS policy_bound (
  policy_key TEXT PRIMARY KEY,
  environment_key TEXT NOT NULL,
  cohort TEXT,
  model TEXT,
  runtime TEXT,
  accelerator TEXT,
  protocol TEXT,
  concurrency_cap INTEGER NOT NULL,
  sla_p99_ms REAL,
  source TEXT NOT NULL,
  confidence TEXT NOT NULL,
  evidence_json TEXT,
  notes TEXT,
  enforce_enabled INTEGER NOT NULL DEFAULT 0,
  adopted_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workload_run_trace ON workload_run(trace_id);
CREATE INDEX IF NOT EXISTS idx_workload_run_env ON workload_run(environment_key);
CREATE INDEX IF NOT EXISTS idx_request_run ON request_sample(run_uuid);
CREATE INDEX IF NOT EXISTS idx_accel_run ON accelerator_sample(run_uuid);
CREATE INDEX IF NOT EXISTS idx_decision_trace ON placement_decision(trace_id);
CREATE INDEX IF NOT EXISTS idx_ref_profile_key ON reference_profile(profile_key);
CREATE INDEX IF NOT EXISTS idx_policy_env ON policy_bound(environment_key);
