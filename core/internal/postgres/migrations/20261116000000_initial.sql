CREATE TABLE core.core_principals (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200 AND btrim(name) = name),
  active boolean NOT NULL DEFAULT true,
  can_manage_administrators boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.core_bootstrap (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  principal_id uuid NOT NULL UNIQUE REFERENCES core.core_principals(id)
);

CREATE TABLE core.core_credentials (
  id uuid PRIMARY KEY,
  principal_id uuid NOT NULL REFERENCES core.core_principals(id),
  secret_hash bytea NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
  expires_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX core_credentials_active_principal_idx ON core.core_credentials (principal_id)
  WHERE revoked_at IS NULL;

CREATE TABLE core.core_access_groups (
  group_key text PRIMARY KEY CHECK (group_key ~ '^[a-z][a-z0-9_-]{0,63}$'),
  active boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core.core_group_access (
  principal_id uuid NOT NULL REFERENCES core.core_principals(id) ON DELETE CASCADE,
  group_key text NOT NULL REFERENCES core.core_access_groups(group_key),
  access_level smallint NOT NULL CHECK (access_level BETWEEN 0 AND 2),
  PRIMARY KEY (principal_id, group_key)
);

CREATE INDEX core_group_access_group_idx ON core.core_group_access (group_key, principal_id, access_level);

CREATE TABLE core.core_objects (
  id uuid PRIMARY KEY,
  object_key uuid NOT NULL UNIQUE,
  metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
  content_type text NOT NULL CHECK (length(content_type) BETWEEN 3 AND 255),
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  sha256 bytea CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting', 'failed')),
  CHECK (state <> 'ready' OR sha256 IS NOT NULL),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX core_objects_ready_cursor_idx ON core.core_objects (id) WHERE state = 'ready';
CREATE INDEX core_objects_metadata_idx ON core.core_objects USING gin (metadata jsonb_path_ops);
CREATE INDEX core_objects_pending_idx ON core.core_objects (state, created_at) WHERE state IN ('pending', 'deleting', 'failed');

CREATE TABLE core.core_file_groups (
  object_id uuid NOT NULL REFERENCES core.core_objects(id) ON DELETE CASCADE,
  group_key text NOT NULL REFERENCES core.core_access_groups(group_key),
  PRIMARY KEY (object_id, group_key)
);

CREATE INDEX core_file_groups_group_cursor_idx ON core.core_file_groups (group_key, object_id);

CREATE TABLE core.core_upload_requests (
  principal_id uuid NOT NULL REFERENCES core.core_principals(id),
  request_key uuid NOT NULL,
  request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
  object_id uuid REFERENCES core.core_objects(id) ON DELETE SET NULL,
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'failed', 'deleted')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (principal_id, request_key)
);

CREATE INDEX core_upload_requests_cleanup_idx ON core.core_upload_requests (state, updated_at)
  WHERE state IN ('pending', 'failed');

-- Staged objects are opaque byte handles for Loader. They contain no session,
-- part number, expiry policy, or workflow state owned by the Loader module.
CREATE TABLE core.core_staged_objects (
  id uuid PRIMARY KEY,
  object_key uuid NOT NULL UNIQUE,
  principal_id uuid NOT NULL REFERENCES core.core_principals(id),
  group_keys text[] NOT NULL CHECK (cardinality(group_keys) > 0),
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting', 'failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX core_staged_objects_cleanup_idx ON core.core_staged_objects (state, updated_at);

CREATE TABLE core.core_file_sets (
  id uuid PRIMARY KEY,
  created_by uuid NOT NULL REFERENCES core.core_principals(id),
  namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9-]{0,63}$'),
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200 AND btrim(name) = name),
  management_group text NOT NULL REFERENCES core.core_access_groups(group_key),
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (namespace, name)
);

CREATE TABLE core.core_file_set_members (
  file_set_id uuid NOT NULL REFERENCES core.core_file_sets(id) ON DELETE CASCADE,
  object_id uuid NOT NULL REFERENCES core.core_objects(id) ON DELETE CASCADE,
  added_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (file_set_id, object_id)
);

CREATE INDEX core_file_set_members_object_idx ON core.core_file_set_members (object_id, file_set_id);

CREATE SEQUENCE core.module_data_revision_seq AS bigint;

CREATE TABLE core.module_data (
  module_id text NOT NULL CHECK (module_id ~ '^[a-z][a-z0-9-]{0,63}$'),
  namespace text NOT NULL CHECK (length(namespace) <= 255 AND namespace ~ '^[a-z][a-z0-9_-]{0,63}(/[a-z][a-z0-9_-]{0,63})*$'),
  scope_type text NOT NULL CHECK (scope_type ~ '^[a-z][a-z0-9_-]{0,31}$'),
  scope_id text NOT NULL CHECK (length(scope_id) BETWEEN 1 AND 256),
  record_key text NOT NULL CHECK (length(record_key) BETWEEN 1 AND 256),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  payload_version integer NOT NULL CHECK (payload_version > 0),
  index_data jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(index_data) = 'object'),
  revision bigint NOT NULL DEFAULT nextval('core.module_data_revision_seq') CHECK (revision > 0),
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (module_id, namespace, scope_type, scope_id, record_key)
);

CREATE INDEX module_data_indexes_gin_idx ON core.module_data USING gin (index_data jsonb_path_ops);
CREATE INDEX module_data_expiry_idx ON core.module_data (module_id, namespace, scope_type, scope_id, expires_at, record_key)
  WHERE expires_at IS NOT NULL;

-- Coordinator state is Core-owned so replicas, workers, and publication
-- transitions share one database clock and one transaction boundary.
CREATE TABLE core.coordinator_config (
  system_id text PRIMARY KEY CHECK (length(system_id) BETWEEN 1 AND 128),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  epoch bigint NOT NULL CHECK (epoch > 0),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE core.coordinator_instances (
  system_id text NOT NULL REFERENCES core.coordinator_config(system_id) ON DELETE CASCADE,
  instance_id uuid NOT NULL,
  config_epoch bigint NOT NULL CHECK (config_epoch > 0),
  lease_until timestamptz NOT NULL,
  heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (system_id, instance_id)
);
CREATE INDEX coordinator_instances_lease_idx ON core.coordinator_instances (system_id, lease_until);

CREATE TABLE core.coordinator_jobs (
  job_id uuid PRIMARY KEY,
  system_id text NOT NULL REFERENCES core.coordinator_config(system_id) ON DELETE CASCADE,
  job_type text NOT NULL CHECK (job_type ~ '^[a-z][a-z0-9._/-]{0,127}$'),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  state text NOT NULL CHECK (state IN ('queued','running','succeeded','failed','cancelled')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_owner uuid,
  lease_until timestamptz,
  fencing_token bigint NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
  claim_epoch bigint NOT NULL DEFAULT 0 CHECK (claim_epoch >= 0),
  result jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
  error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9._-]{0,127}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (system_id, job_type, idempotency_key),
  CHECK ((state = 'running' AND lease_owner IS NOT NULL AND lease_until IS NOT NULL)
    OR (state <> 'running' AND lease_owner IS NULL AND lease_until IS NULL))
);
CREATE INDEX coordinator_jobs_claim_idx ON core.coordinator_jobs (system_id, state, available_at, created_at);
CREATE INDEX coordinator_jobs_lease_idx ON core.coordinator_jobs (system_id, lease_until) WHERE state = 'running';

CREATE TABLE core.coordinator_publications (
  system_id text NOT NULL REFERENCES core.coordinator_config(system_id) ON DELETE CASCADE,
  route_id uuid NOT NULL,
  input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[a-f0-9]{64}$'),
  generation bigint NOT NULL CHECK (generation > 0),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (system_id, route_id)
);

CREATE TABLE core.coordinator_route_targets (
  system_id text NOT NULL REFERENCES core.coordinator_config(system_id) ON DELETE CASCADE,
  route_id uuid NOT NULL,
  file_id uuid NOT NULL REFERENCES core.core_objects(id) ON DELETE RESTRICT,
  revision bigint NOT NULL CHECK (revision > 0),
  publication_generation bigint NOT NULL DEFAULT 0 CHECK (publication_generation >= 0),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (system_id, route_id)
);

-- Runtime connections use a dedicated NOLOGIN group role and do not own the
-- schema or Atlas revision table. Database operators provision the role before
-- applying migrations; each future migration grants access only to its tables.
GRANT USAGE ON SCHEMA core TO zubrium_core_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON
  core.core_principals,
  core.core_bootstrap,
  core.core_credentials,
  core.core_access_groups,
  core.core_group_access,
  core.core_objects,
  core.core_file_groups,
  core.core_upload_requests,
  core.core_staged_objects,
  core.core_file_sets,
  core.core_file_set_members,
  core.module_data,
  core.coordinator_config,
  core.coordinator_instances,
  core.coordinator_jobs,
  core.coordinator_publications,
  core.coordinator_route_targets
TO zubrium_core_runtime;
GRANT USAGE, SELECT ON SEQUENCE core.module_data_revision_seq TO zubrium_core_runtime;
