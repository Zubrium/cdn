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
  revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
  state text NOT NULL CHECK (state IN ('pending', 'ready', 'deleting', 'failed')),
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
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (namespace, name)
);

CREATE TABLE core.core_file_set_members (
  file_set_id uuid NOT NULL REFERENCES core.core_file_sets(id) ON DELETE CASCADE,
  object_id uuid NOT NULL REFERENCES core.core_objects(id) ON DELETE CASCADE,
  added_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (file_set_id, object_id)
);

CREATE INDEX core_file_set_members_object_idx ON core.core_file_set_members (object_id, file_set_id);

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
  core.core_file_set_members
TO zubrium_core_runtime;
