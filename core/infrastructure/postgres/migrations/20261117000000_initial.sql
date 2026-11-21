CREATE SCHEMA access;
CREATE SCHEMA files;
CREATE SCHEMA uploads;
CREATE SCHEMA file_index;
CREATE SCHEMA engine_data;
CREATE SCHEMA coordination;

CREATE TABLE access.users (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200 AND btrim(name) = name),
  active boolean NOT NULL DEFAULT true,
  can_manage_administrators boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE access.bootstrap (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  user_id uuid NOT NULL UNIQUE REFERENCES access.users(id)
);

CREATE TABLE access.credentials (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES access.users(id),
  secret_hash bytea NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
  expires_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX credentials_active_user_idx ON access.credentials (user_id)
  WHERE revoked_at IS NULL;

CREATE TABLE access.sessions (
  id uuid PRIMARY KEY,
  credential_id uuid NOT NULL REFERENCES access.credentials(id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  created_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  idle_expires_at timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  CHECK (created_at <= last_seen_at),
  CHECK (last_seen_at < absolute_expires_at),
  CHECK (idle_expires_at <= absolute_expires_at),
  CHECK (created_at < absolute_expires_at)
);
CREATE INDEX sessions_credential_idx ON access.sessions (credential_id);
CREATE INDEX sessions_expiry_idx ON access.sessions (idle_expires_at, absolute_expires_at)
  WHERE revoked_at IS NULL;
CREATE INDEX sessions_revoked_idx ON access.sessions (revoked_at) WHERE revoked_at IS NOT NULL;

CREATE TABLE access.groups (
  group_key text PRIMARY KEY CHECK (group_key ~ '^[a-z][a-z0-9_-]{0,63}$'),
  active boolean NOT NULL DEFAULT false
);

CREATE TABLE access.grants (
  user_id uuid NOT NULL REFERENCES access.users(id) ON DELETE CASCADE,
  group_key text NOT NULL REFERENCES access.groups(group_key),
  access_level smallint NOT NULL CHECK (access_level BETWEEN 0 AND 2),
  PRIMARY KEY (user_id, group_key)
);
CREATE INDEX grants_group_lookup_idx ON access.grants (group_key, user_id, access_level);

CREATE TABLE files.objects (
  id uuid PRIMARY KEY,
  object_key uuid NOT NULL UNIQUE,
  metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
  content_type text NOT NULL CHECK (length(content_type) BETWEEN 3 AND 255),
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  sha256 bytea CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting', 'failed')),
  CHECK (state <> 'ready' OR sha256 IS NOT NULL),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX objects_ready_cursor_idx ON files.objects (id) WHERE state = 'ready';
CREATE INDEX objects_metadata_idx ON files.objects USING gin (metadata jsonb_path_ops);
CREATE INDEX objects_pending_idx ON files.objects (updated_at, id) WHERE state IN ('pending', 'deleting', 'failed');

CREATE TABLE files.memberships (
  object_id uuid NOT NULL REFERENCES files.objects(id) ON DELETE CASCADE,
  group_key text NOT NULL REFERENCES access.groups(group_key),
  PRIMARY KEY (object_id, group_key)
);
CREATE INDEX memberships_group_cursor_idx ON files.memberships (group_key, object_id);

CREATE TABLE uploads.requests (
  user_id uuid NOT NULL REFERENCES access.users(id),
  request_key uuid NOT NULL,
  request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
  object_id uuid REFERENCES files.objects(id) ON DELETE SET NULL,
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'failed', 'deleted')),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, request_key)
);
CREATE INDEX requests_cleanup_idx ON uploads.requests (state, updated_at)
  WHERE state IN ('pending', 'failed');

-- Opaque temporary bytes belong to Core; resumable upload sessions remain
-- engine-owned documents in engine_data.records.
CREATE TABLE uploads.staged (
  id uuid PRIMARY KEY,
  object_key uuid NOT NULL UNIQUE,
  user_id uuid NOT NULL REFERENCES access.users(id),
  group_keys text[] NOT NULL CHECK (cardinality(group_keys) > 0),
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting', 'failed')),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX staged_cleanup_idx ON uploads.staged (state, updated_at);

CREATE TABLE file_index.sets (
  id uuid PRIMARY KEY,
  namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9-]{0,63}$'),
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200 AND btrim(name) = name),
  management_group text NOT NULL REFERENCES access.groups(group_key),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (namespace, name)
);

CREATE TABLE file_index.members (
  set_id uuid NOT NULL REFERENCES file_index.sets(id) ON DELETE CASCADE,
  object_id uuid NOT NULL REFERENCES files.objects(id) ON DELETE CASCADE,
  PRIMARY KEY (set_id, object_id)
);
CREATE INDEX members_object_lookup_idx ON file_index.members (object_id, set_id);

CREATE SEQUENCE engine_data.revision_seq AS bigint;
CREATE TABLE engine_data.records (
  engine_id text NOT NULL CHECK (engine_id ~ '^[a-z][a-z0-9.-]{0,63}$'),
  namespace text NOT NULL CHECK (length(namespace) <= 255 AND namespace ~ '^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})*$'),
  scope_type text NOT NULL CHECK (scope_type ~ '^[a-z][a-z0-9_-]{0,31}$'),
  scope_id text NOT NULL CHECK (length(scope_id) BETWEEN 1 AND 256),
  record_key text NOT NULL CHECK (length(record_key) BETWEEN 1 AND 256),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  payload_version integer NOT NULL CHECK (payload_version > 0),
  index_data jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(index_data) = 'object'),
  revision bigint NOT NULL DEFAULT nextval('engine_data.revision_seq') CHECK (revision > 0),
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (engine_id, namespace, scope_type, scope_id, record_key)
);
CREATE INDEX records_indexes_gin_idx ON engine_data.records USING gin (index_data jsonb_path_ops);
CREATE INDEX records_expiry_idx ON engine_data.records (engine_id, namespace, scope_type, scope_id, expires_at, record_key)
  WHERE expires_at IS NOT NULL;

CREATE TABLE coordination.runtime (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  epoch bigint NOT NULL CHECK (epoch > 0)
);

CREATE TABLE coordination.instances (
  instance_id uuid PRIMARY KEY,
  config_epoch bigint NOT NULL CHECK (config_epoch > 0),
  lease_until timestamptz NOT NULL
);
CREATE INDEX instances_lease_idx ON coordination.instances (lease_until);

CREATE TABLE coordination.workflows (
  workflow_id uuid PRIMARY KEY,
  root_task_id uuid NOT NULL,
  owner_engine_id text NOT NULL CHECK (owner_engine_id ~ '^[a-z][a-z0-9.-]{0,63}$'),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
  input_hash bytea NOT NULL CHECK (octet_length(input_hash) = 32),
  config_epoch bigint NOT NULL CHECK (config_epoch > 0),
  state text NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  result jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz,
  UNIQUE (owner_engine_id, idempotency_key)
);
CREATE INDEX workflows_state_idx ON coordination.workflows (state, updated_at);
CREATE INDEX workflows_expiry_idx ON coordination.workflows (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE coordination.tasks (
  task_id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES coordination.workflows(workflow_id) ON DELETE CASCADE,
  task_kind text NOT NULL CHECK (task_kind IN ('worker', 'join')),
  component_id text CHECK (component_id ~ '^[a-z][a-z0-9.-]{0,63}$'),
  operation_id text CHECK (operation_id ~ '^[a-z][a-z0-9._/-]{0,127}$'),
  contract_version integer CHECK (contract_version > 0),
  parent_task_id uuid,
  emission_key text CHECK (emission_key IS NULL OR length(emission_key) BETWEEN 1 AND 256),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  result jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
  state text NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_owner uuid,
  lease_until timestamptz,
  fencing_token bigint NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
  config_epoch bigint NOT NULL CHECK (config_epoch > 0),
  error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9._-]{0,127}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (workflow_id, task_id),
  FOREIGN KEY (workflow_id, parent_task_id) REFERENCES coordination.tasks(workflow_id, task_id),
  CHECK ((task_kind = 'worker' AND component_id IS NOT NULL AND operation_id IS NOT NULL AND contract_version IS NOT NULL)
    OR (task_kind = 'join' AND component_id IS NULL AND operation_id IS NULL AND contract_version IS NULL)),
  CHECK ((parent_task_id IS NULL AND emission_key IS NULL) OR (parent_task_id IS NOT NULL AND emission_key IS NOT NULL)),
  CHECK ((state = 'running' AND lease_owner IS NOT NULL AND lease_until IS NOT NULL)
    OR (state <> 'running' AND lease_owner IS NULL AND lease_until IS NULL))
);
CREATE UNIQUE INDEX tasks_emission_idempotency_idx ON coordination.tasks (workflow_id, parent_task_id, emission_key)
  WHERE parent_task_id IS NOT NULL;
CREATE INDEX tasks_claim_idx ON coordination.tasks (state, available_at, created_at)
  WHERE state = 'queued' AND task_kind = 'worker';
CREATE INDEX tasks_lease_idx ON coordination.tasks (lease_until) WHERE state = 'running';
CREATE INDEX tasks_workflow_idx ON coordination.tasks (workflow_id, created_at, task_id);

CREATE TABLE coordination.dependencies (
  workflow_id uuid NOT NULL,
  task_id uuid NOT NULL,
  depends_on uuid NOT NULL,
  PRIMARY KEY (workflow_id, task_id, depends_on),
  FOREIGN KEY (workflow_id, task_id) REFERENCES coordination.tasks(workflow_id, task_id) ON DELETE CASCADE,
  FOREIGN KEY (workflow_id, depends_on) REFERENCES coordination.tasks(workflow_id, task_id) ON DELETE CASCADE,
  CHECK (task_id <> depends_on)
);
CREATE INDEX dependencies_parent_idx ON coordination.dependencies (workflow_id, depends_on, task_id);

GRANT USAGE ON SCHEMA access, files, uploads, file_index, engine_data, coordination TO zubrium_core_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON
  access.users,
  access.bootstrap,
  access.credentials,
  access.sessions,
  access.groups,
  access.grants,
  files.objects,
  files.memberships,
  uploads.requests,
  uploads.staged,
  file_index.sets,
  file_index.members,
  engine_data.records,
  coordination.runtime,
  coordination.instances,
  coordination.workflows,
  coordination.tasks,
  coordination.dependencies
TO zubrium_core_runtime;
GRANT USAGE, SELECT ON SEQUENCE engine_data.revision_seq TO zubrium_core_runtime;
