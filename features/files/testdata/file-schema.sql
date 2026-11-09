CREATE TABLE file_owners (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('admin','service')),
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    enabled boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind,name)
);
CREATE UNIQUE INDEX file_owners_one_admin ON file_owners(kind) WHERE kind='admin';
INSERT INTO file_owners(id,kind,name,enabled) VALUES(gen_random_uuid(),'admin','admin',true);
CREATE FUNCTION admin_file_owner() RETURNS uuid LANGUAGE SQL STABLE AS $$
    SELECT id FROM file_owners WHERE kind='admin'
$$;
ALTER TABLE media_objects ADD COLUMN owner_id uuid NOT NULL DEFAULT admin_file_owner() REFERENCES file_owners(id);
ALTER TABLE media_objects ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','private'));
ALTER TABLE media_objects ADD COLUMN sha256 text CHECK (sha256 IS NULL OR sha256 ~ '^[a-f0-9]{64}$');
CREATE INDEX media_objects_owner ON media_objects(owner_id,id);

CREATE TABLE file_upload_sessions (
    id uuid PRIMARY KEY,
    file_id uuid NOT NULL UNIQUE,
    owner_id uuid NOT NULL REFERENCES file_owners(id),
    name text NOT NULL,
    size_bytes bigint NOT NULL CHECK(size_bytes>0 AND size_bytes<=5368709120),
    sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
    visibility text NOT NULL CHECK(visibility IN ('public','private')),
    part_bytes bigint NOT NULL CHECK(part_bytes>0),
    state text NOT NULL CHECK(state IN ('uploading','complete','cancelled')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX file_upload_sessions_cleanup ON file_upload_sessions(state,expires_at);
CREATE TABLE file_upload_parts (
    session_id uuid NOT NULL REFERENCES file_upload_sessions(id) ON DELETE CASCADE,
    number integer NOT NULL CHECK(number>0),
    object_key text NOT NULL UNIQUE,
    size_bytes bigint NOT NULL CHECK(size_bytes>0),
    sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
    ready boolean NOT NULL,
    PRIMARY KEY(session_id,number)
);
CREATE TABLE file_object_deletions (
    object_key text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
