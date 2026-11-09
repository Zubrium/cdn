-- Initial schema for the branch and snapshot model. Existing installations require a new empty database.
--
-- PostgreSQL database dump
--


-- Dumped from database version 16.15
-- Dumped by pg_dump version 16.15

SET check_function_bodies = false;

--
-- Name: admin_file_owner(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.admin_file_owner() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$
    SELECT id FROM file_owners WHERE kind='admin'
$$;


--
-- Name: allow_file_reference_clear(jsonb, jsonb, text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.allow_file_reference_clear(old_value jsonb, new_value jsonb, file_id text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE STRICT
    AS $$
DECLARE
    key text;
    old_item jsonb;
    new_item jsonb;
    i integer;
BEGIN
    IF old_value = new_value THEN RETURN true; END IF;
    IF old_value = to_jsonb(file_id) AND new_value = to_jsonb(''::text) THEN RETURN true; END IF;
    IF jsonb_typeof(old_value) <> jsonb_typeof(new_value) THEN RETURN false; END IF;
    IF jsonb_typeof(old_value) = 'object' THEN
        IF (SELECT count(*) FROM jsonb_object_keys(old_value)) <> (SELECT count(*) FROM jsonb_object_keys(new_value)) THEN RETURN false; END IF;
        FOR key, old_item IN SELECT * FROM jsonb_each(old_value) LOOP
            IF NOT new_value ? key THEN RETURN false; END IF;
            new_item := new_value -> key;
            IF NOT allow_file_reference_clear(old_item,new_item,file_id) THEN RETURN false; END IF;
        END LOOP;
        RETURN true;
    ELSIF jsonb_typeof(old_value) = 'array' THEN
        IF jsonb_array_length(old_value) <> jsonb_array_length(new_value) THEN RETURN false; END IF;
        FOR i IN 0..(jsonb_array_length(old_value)-1) LOOP
            IF NOT allow_file_reference_clear(old_value -> i,new_value -> i,file_id) THEN RETURN false; END IF;
        END LOOP;
        RETURN true;
    END IF;
    RETURN false;
END;
$$;


--
-- Name: protect_snapshot_structure(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.protect_snapshot_structure() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE file_id text;
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.sequence IS DISTINCT FROM OLD.sequence OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'snapshot identity is immutable' USING ERRCODE='check_violation';
    END IF;
    file_id := NULLIF(current_setting('websiteengine.prune_file',true),'');
    IF NEW.payload IS DISTINCT FROM OLD.payload THEN
        IF file_id IS NULL OR NOT allow_file_reference_clear(OLD.payload,NEW.payload,file_id) THEN
            RAISE EXCEPTION 'snapshot structure is immutable' USING ERRCODE='check_violation';
        END IF;
    ELSIF NEW.digest IS DISTINCT FROM OLD.digest THEN
        RAISE EXCEPTION 'snapshot digest is immutable without content removal' USING ERRCODE='check_violation';
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: reject_site_artifact_mutation(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.reject_site_artifact_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND TG_TABLE_NAME IN ('feature_builds', 'site_snapshots')
       AND (to_jsonb(OLD) -> 'created_by') IS NOT NULL
       AND (to_jsonb(OLD) -> 'created_by') <> 'null'::jsonb
       AND (to_jsonb(NEW) -> 'created_by') = 'null'::jsonb
       AND (to_jsonb(NEW) - 'created_by') = (to_jsonb(OLD) - 'created_by') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME USING ERRCODE = 'check_violation';
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--

-- Tables and constraints are declared together in this fresh initial schema.

-- access_tokens
CREATE TABLE public.access_tokens (
    id uuid NOT NULL,
    name text NOT NULL,
    token_hash bytea NOT NULL,
    role text NOT NULL,
    created_by uuid,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT access_tokens_expiry_check CHECK (((expires_at IS NULL) OR (expires_at > created_at))),
    CONSTRAINT access_tokens_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 120))),
    CONSTRAINT access_tokens_role_check CHECK ((role = ANY (ARRAY['admin'::text, 'moderator'::text, 'staff'::text, 'user'::text]))),
    CONSTRAINT access_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT fk_access_tokens_creator FOREIGN KEY (created_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL
);

-- file_object_deletions
CREATE TABLE public.file_object_deletions (
    object_key text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT file_object_deletions_pkey PRIMARY KEY (object_key)
);

-- file_owners
CREATE TABLE public.file_owners (
    id uuid NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    enabled boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT file_owners_kind_check CHECK ((kind = ANY (ARRAY['admin'::text, 'service'::text]))),
    CONSTRAINT file_owners_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 100))),
    CONSTRAINT file_owners_kind_name_key UNIQUE (kind, name),
    CONSTRAINT file_owners_pkey PRIMARY KEY (id)
);

-- site_settings
CREATE TABLE public.site_settings (
    id boolean DEFAULT true NOT NULL,
    name text DEFAULT 'Website'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT site_settings_id_check CHECK (id),
    CONSTRAINT site_settings_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 200))),
    CONSTRAINT site_settings_pkey PRIMARY KEY (id)
);

-- features
CREATE TABLE public.features (
    id uuid NOT NULL,
    key text NOT NULL,
    name text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT features_key_check CHECK ((key ~ '^[a-z][a-z0-9_-]{0,99}$'::text)),
    CONSTRAINT features_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 200))),
    CONSTRAINT features_revision_check CHECK ((revision > 0)),
    CONSTRAINT features_status_check CHECK ((status = ANY (ARRAY['active'::text, 'archived'::text]))),
    CONSTRAINT features_pkey PRIMARY KEY (id),
    CONSTRAINT fk_features_creator FOREIGN KEY (created_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL
);

-- file_upload_sessions
CREATE TABLE public.file_upload_sessions (
    id uuid NOT NULL,
    file_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    name text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    visibility text NOT NULL,
    part_bytes bigint NOT NULL,
    state text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT file_upload_sessions_part_bytes_check CHECK ((part_bytes > 0)),
    CONSTRAINT file_upload_sessions_sha256_check CHECK ((sha256 ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT file_upload_sessions_size_bytes_check CHECK (((size_bytes > 0) AND (size_bytes <= '5368709120'::bigint))),
    CONSTRAINT file_upload_sessions_state_check CHECK ((state = ANY (ARRAY['uploading'::text, 'complete'::text, 'cancelled'::text]))),
    CONSTRAINT file_upload_sessions_visibility_check CHECK ((visibility = ANY (ARRAY['public'::text, 'private'::text]))),
    CONSTRAINT file_upload_sessions_file_id_key UNIQUE (file_id),
    CONSTRAINT file_upload_sessions_pkey PRIMARY KEY (id),
    CONSTRAINT file_upload_sessions_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.file_owners(id)
);

-- media_objects
CREATE TABLE public.media_objects (
    id uuid NOT NULL,
    object_key text NOT NULL,
    original_filename text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL,
    status text DEFAULT 'processing'::text NOT NULL,
    variants jsonb DEFAULT '{}'::jsonb NOT NULL,
    uploaded_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner_id uuid DEFAULT public.admin_file_owner() NOT NULL,
    visibility text DEFAULT 'public'::text NOT NULL,
    sha256 text,
    CONSTRAINT media_objects_filename_check CHECK (((length(original_filename) >= 1) AND (length(original_filename) <= 255))),
    CONSTRAINT media_objects_object_key_check CHECK (((length(object_key) >= 1) AND (length(object_key) <= 1024))),
    CONSTRAINT media_objects_sha256_check CHECK (((sha256 IS NULL) OR (sha256 ~ '^[a-f0-9]{64}$'::text))),
    CONSTRAINT media_objects_size_check CHECK ((size_bytes >= 0)),
    CONSTRAINT media_objects_status_check CHECK ((status = ANY (ARRAY['processing'::text, 'ready'::text, 'failed'::text, 'deleted'::text]))),
    CONSTRAINT media_objects_variants_check CHECK ((jsonb_typeof(variants) = 'object'::text)),
    CONSTRAINT media_objects_visibility_check CHECK ((visibility = ANY (ARRAY['public'::text, 'private'::text]))),
    CONSTRAINT media_objects_pkey PRIMARY KEY (id),
    CONSTRAINT fk_media_objects_uploader FOREIGN KEY (uploaded_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT media_objects_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.file_owners(id)
);

-- site_snapshots
CREATE TABLE public.site_snapshots (
    id uuid NOT NULL,
    sequence bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE CACHE 1) NOT NULL,
    name text NOT NULL,
    digest text NOT NULL,
    payload jsonb NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    parent_snapshot_id uuid,
    content_revision bigint DEFAULT 0 NOT NULL,
    CONSTRAINT site_snapshots_content_revision_check CHECK ((content_revision >= 0)),
    CONSTRAINT site_snapshots_digest_check CHECK ((digest ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT site_snapshots_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 160))),
    CONSTRAINT site_snapshots_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT site_snapshots_sequence_check CHECK ((sequence > 0)),
    CONSTRAINT site_snapshots_pkey PRIMARY KEY (id),
    CONSTRAINT fk_site_snapshots_creator FOREIGN KEY (created_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT fk_site_snapshots_parent FOREIGN KEY (parent_snapshot_id) REFERENCES public.site_snapshots(id) ON DELETE RESTRICT
);

-- feature_versions
CREATE TABLE public.feature_versions (
    id uuid NOT NULL,
    feature_id uuid NOT NULL,
    version integer NOT NULL,
    schema jsonb NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    CONSTRAINT feature_versions_published_at_check CHECK ((((status = 'draft'::text) AND (published_at IS NULL)) OR ((status = 'published'::text) AND (published_at IS NOT NULL)))),
    CONSTRAINT feature_versions_schema_check CHECK ((jsonb_typeof(schema) = 'object'::text)),
    CONSTRAINT feature_versions_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text]))),
    CONSTRAINT feature_versions_version_check CHECK ((version > 0)),
    CONSTRAINT feature_versions_pkey PRIMARY KEY (id),
    CONSTRAINT fk_feature_versions_creator FOREIGN KEY (created_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT fk_feature_versions_feature FOREIGN KEY (feature_id) REFERENCES public.features(id) ON DELETE RESTRICT
);

-- file_upload_parts
CREATE TABLE public.file_upload_parts (
    session_id uuid NOT NULL,
    number integer NOT NULL,
    object_key text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    ready boolean NOT NULL,
    CONSTRAINT file_upload_parts_number_check CHECK ((number > 0)),
    CONSTRAINT file_upload_parts_sha256_check CHECK ((sha256 ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT file_upload_parts_size_bytes_check CHECK ((size_bytes > 0)),
    CONSTRAINT file_upload_parts_object_key_key UNIQUE (object_key),
    CONSTRAINT file_upload_parts_pkey PRIMARY KEY (session_id, number),
    CONSTRAINT file_upload_parts_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.file_upload_sessions(id) ON DELETE CASCADE
);

-- media_renditions
CREATE TABLE public.media_renditions (
    media_id uuid NOT NULL,
    profile_hash text NOT NULL,
    profile jsonb NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    outputs jsonb DEFAULT '{}'::jsonb NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_expires_at timestamp with time zone,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_renditions_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT media_renditions_outputs_check CHECK ((jsonb_typeof(outputs) = 'object'::text)),
    CONSTRAINT media_renditions_profile_check CHECK ((jsonb_typeof(profile) = 'object'::text)),
    CONSTRAINT media_renditions_profile_hash_check CHECK ((profile_hash ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT media_renditions_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'ready'::text, 'failed'::text]))),
    CONSTRAINT media_renditions_pkey PRIMARY KEY (media_id, profile_hash),
    CONSTRAINT fk_media_renditions_media FOREIGN KEY (media_id) REFERENCES public.media_objects(id) ON DELETE CASCADE
);

-- site_environments
CREATE TABLE public.site_environments (
    key text NOT NULL,
    name text NOT NULL,
    public_base_url text DEFAULT ''::text NOT NULL,
    http_port integer NOT NULL,
    cors_origins text[] DEFAULT '{}'::text[] NOT NULL,
    active_snapshot_id uuid,
    revision bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    indexable boolean DEFAULT false NOT NULL,
    http_addr text DEFAULT ''::text NOT NULL,
    CONSTRAINT site_environments_name_check CHECK (((length(TRIM(BOTH FROM name)) >= 1) AND (length(TRIM(BOTH FROM name)) <= 100))),
    CONSTRAINT site_environments_port_check CHECK (((http_port >= 1) AND (http_port <= 65535))),
    CONSTRAINT site_environments_revision_check CHECK ((revision > 0)),
    CONSTRAINT site_environments_pkey PRIMARY KEY (key),
    CONSTRAINT fk_site_environments_snapshot FOREIGN KEY (active_snapshot_id) REFERENCES public.site_snapshots(id) ON DELETE SET NULL
);

-- site_snapshot_events
CREATE TABLE public.site_snapshot_events (
    id uuid NOT NULL,
    snapshot_id uuid,
    environment text NOT NULL,
    action text NOT NULL,
    actor_token_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    snapshot_name text DEFAULT ''::text NOT NULL,
    snapshot_sequence bigint DEFAULT 0 NOT NULL,
    CONSTRAINT site_snapshot_events_action_check CHECK ((action = ANY (ARRAY['publish'::text, 'promote'::text, 'rollback'::text, 'deploy'::text]))),
    CONSTRAINT site_snapshot_events_pkey PRIMARY KEY (id),
    CONSTRAINT fk_site_snapshot_events_actor FOREIGN KEY (actor_token_id) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT fk_site_snapshot_events_snapshot FOREIGN KEY (snapshot_id) REFERENCES public.site_snapshots(id) ON DELETE SET NULL
);

-- site_snapshot_model_workspaces
CREATE TABLE public.site_snapshot_model_workspaces (
    snapshot_id uuid NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    published_revision bigint DEFAULT 0 NOT NULL,
    draft_records jsonb DEFAULT '[]'::jsonb NOT NULL,
    draft_selections jsonb DEFAULT '[]'::jsonb NOT NULL,
    published_records jsonb DEFAULT '[]'::jsonb NOT NULL,
    published_selections jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_by uuid,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT site_snapshot_model_workspaces_pkey PRIMARY KEY (snapshot_id),
    CONSTRAINT site_snapshot_model_workspaces_revision_check CHECK (((revision >= 0) AND (published_revision >= 0) AND (published_revision <= revision))),
    CONSTRAINT site_snapshot_model_workspaces_draft_records_check CHECK ((jsonb_typeof(draft_records) = 'array'::text)),
    CONSTRAINT site_snapshot_model_workspaces_draft_selections_check CHECK ((jsonb_typeof(draft_selections) = 'array'::text)),
    CONSTRAINT site_snapshot_model_workspaces_published_records_check CHECK ((jsonb_typeof(published_records) = 'array'::text)),
    CONSTRAINT site_snapshot_model_workspaces_published_selections_check CHECK ((jsonb_typeof(published_selections) = 'array'::text)),
    CONSTRAINT fk_site_snapshot_model_workspaces_snapshot FOREIGN KEY (snapshot_id) REFERENCES public.site_snapshots(id) ON DELETE CASCADE,
    CONSTRAINT fk_site_snapshot_model_workspaces_updater FOREIGN KEY (updated_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL
);

-- snapshot_model_content_publications
CREATE TABLE public.snapshot_model_content_publications (
    id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    draft_revision bigint NOT NULL,
    content_revision bigint NOT NULL,
    records jsonb NOT NULL,
    selections jsonb NOT NULL,
    actor_token_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT snapshot_model_content_publications_records_check CHECK ((jsonb_typeof(records) = 'array'::text)),
    CONSTRAINT snapshot_model_content_publications_selections_check CHECK ((jsonb_typeof(selections) = 'array'::text)),
    CONSTRAINT snapshot_model_content_publications_snapshot_revision_key UNIQUE (snapshot_id, draft_revision),
    CONSTRAINT snapshot_model_content_publications_pkey PRIMARY KEY (id),
    CONSTRAINT fk_snapshot_model_content_publications_snapshot FOREIGN KEY (snapshot_id) REFERENCES public.site_snapshots(id) ON DELETE CASCADE,
    CONSTRAINT fk_snapshot_model_content_publications_actor FOREIGN KEY (actor_token_id) REFERENCES public.access_tokens(id) ON DELETE SET NULL
);

-- feature_builds
CREATE TABLE public.feature_builds (
    id uuid NOT NULL,
    feature_id uuid NOT NULL,
    version text NOT NULL,
    submission_schema_version_id uuid,
    digest text NOT NULL,
    artifact_size bigint NOT NULL,
    manifest jsonb NOT NULL,
    assets jsonb NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT feature_builds_digest_check CHECK ((digest ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT feature_builds_manifest_check CHECK (((jsonb_typeof(manifest) = 'object'::text) AND (jsonb_typeof(assets) = 'object'::text))),
    CONSTRAINT feature_builds_size_check CHECK ((artifact_size > 0)),
    CONSTRAINT feature_builds_version_check CHECK ((version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'::text)),
    CONSTRAINT feature_builds_pkey PRIMARY KEY (id),
    CONSTRAINT fk_feature_builds_creator FOREIGN KEY (created_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT fk_feature_builds_feature FOREIGN KEY (feature_id) REFERENCES public.features(id) ON DELETE RESTRICT,
    CONSTRAINT fk_feature_builds_submission_schema FOREIGN KEY (submission_schema_version_id) REFERENCES public.feature_versions(id) ON DELETE RESTRICT
);

-- form_submissions
CREATE TABLE public.form_submissions (
    id uuid NOT NULL,
    feature_id uuid NOT NULL,
    schema_version_id uuid NOT NULL,
    data jsonb NOT NULL,
    status text DEFAULT 'new'::text NOT NULL,
    processed_by uuid,
    environment text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone,
    deleted_at timestamp with time zone,
    CONSTRAINT submissions_data_check CHECK ((jsonb_typeof(data) = 'object'::text)),
    CONSTRAINT submissions_status_check CHECK ((status = ANY (ARRAY['new'::text, 'in_progress'::text, 'processed'::text, 'rejected'::text]))),
    CONSTRAINT form_submissions_pkey PRIMARY KEY (id),
    CONSTRAINT fk_submissions_feature FOREIGN KEY (feature_id) REFERENCES public.features(id) ON DELETE RESTRICT,
    CONSTRAINT fk_submissions_processor FOREIGN KEY (processed_by) REFERENCES public.access_tokens(id) ON DELETE SET NULL,
    CONSTRAINT fk_submissions_schema FOREIGN KEY (schema_version_id) REFERENCES public.feature_versions(id) ON DELETE RESTRICT
);

-- Seed the single administrative file owner.

CREATE UNIQUE INDEX file_owners_one_admin ON public.file_owners USING btree (kind) WHERE (kind = 'admin'::text);

CREATE INDEX file_upload_sessions_cleanup ON public.file_upload_sessions USING btree (state, expires_at);

CREATE INDEX idx_access_tokens_active ON public.access_tokens USING btree (role, expires_at) WHERE (revoked_at IS NULL);

CREATE INDEX idx_access_tokens_creator ON public.access_tokens USING btree (created_by);

CREATE INDEX idx_feature_builds_feature ON public.feature_builds USING btree (feature_id, created_at DESC);

CREATE INDEX idx_feature_versions_feature ON public.feature_versions USING btree (feature_id, version DESC);

CREATE INDEX idx_media_objects_status_created ON public.media_objects USING btree (status, created_at DESC);

CREATE INDEX idx_media_renditions_lease ON public.media_renditions USING btree (lease_expires_at) WHERE (status = 'processing'::text);

CREATE INDEX idx_media_renditions_queue ON public.media_renditions USING btree (created_at) WHERE (status = 'pending'::text);

CREATE INDEX idx_site_snapshot_events_env ON public.site_snapshot_events USING btree (environment, snapshot_id, created_at DESC);

CREATE INDEX idx_site_snapshots_created ON public.site_snapshots USING btree (created_at DESC);

CREATE INDEX idx_site_snapshots_digest ON public.site_snapshots USING btree (digest);

CREATE INDEX idx_submissions_data ON public.form_submissions USING gin (data jsonb_path_ops) WHERE (deleted_at IS NULL);

CREATE INDEX idx_submissions_environment_created ON public.form_submissions USING btree (environment, created_at DESC) WHERE (deleted_at IS NULL);

CREATE INDEX idx_submissions_feature_created ON public.form_submissions USING btree (feature_id, created_at DESC) WHERE (deleted_at IS NULL);

CREATE INDEX idx_submissions_status_created ON public.form_submissions USING btree (status, created_at DESC) WHERE (deleted_at IS NULL);

CREATE INDEX media_objects_owner ON public.media_objects USING btree (owner_id, id);

CREATE UNIQUE INDEX site_environments_unique_enabled_port ON public.site_environments USING btree (http_port) WHERE enabled;

CREATE UNIQUE INDEX site_environments_unique_name ON public.site_environments USING btree (name);

CREATE UNIQUE INDEX uq_access_tokens_token_hash ON public.access_tokens USING btree (token_hash);

CREATE UNIQUE INDEX uq_feature_builds_digest ON public.feature_builds USING btree (digest);

CREATE UNIQUE INDEX uq_feature_builds_feature_version ON public.feature_builds USING btree (feature_id, version);

CREATE UNIQUE INDEX uq_feature_builds_identity ON public.feature_builds USING btree (id, feature_id);

CREATE UNIQUE INDEX uq_feature_versions_feature_version ON public.feature_versions USING btree (feature_id, version);

CREATE UNIQUE INDEX uq_feature_versions_identity ON public.feature_versions USING btree (id, feature_id);

CREATE UNIQUE INDEX uq_features_key ON public.features USING btree (key);

CREATE UNIQUE INDEX uq_media_objects_object_key ON public.media_objects USING btree (object_key);

CREATE UNIQUE INDEX uq_site_snapshots_sequence ON public.site_snapshots USING btree (sequence);

CREATE TRIGGER feature_builds_are_immutable BEFORE DELETE OR UPDATE ON public.feature_builds FOR EACH ROW EXECUTE FUNCTION public.reject_site_artifact_mutation();

CREATE TRIGGER site_snapshots_structure_is_immutable BEFORE UPDATE ON public.site_snapshots FOR EACH ROW EXECUTE FUNCTION public.protect_snapshot_structure();

INSERT INTO public.file_owners(id,kind,name,enabled) VALUES(gen_random_uuid(),'admin','admin',true);

CREATE INDEX idx_site_snapshot_model_workspaces_draft_updated ON public.site_snapshot_model_workspaces USING btree (updated_at DESC) WHERE (revision > published_revision);

CREATE INDEX idx_snapshot_model_content_publications_history ON public.snapshot_model_content_publications USING btree (snapshot_id, content_revision DESC);
