ALTER TABLE site_snapshots
    ADD COLUMN parent_snapshot_id uuid,
    ADD CONSTRAINT fk_site_snapshots_parent FOREIGN KEY (parent_snapshot_id)
        REFERENCES site_snapshots(id) ON DELETE RESTRICT;

CREATE TABLE site_snapshot_content_drafts (
    snapshot_id uuid NOT NULL,
    feature_key text NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    items jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (snapshot_id, feature_key),
    CONSTRAINT site_snapshot_content_drafts_revision_check CHECK (revision > 0),
    CONSTRAINT site_snapshot_content_drafts_items_check CHECK (jsonb_typeof(items) = 'array'),
    CONSTRAINT site_snapshot_content_drafts_key_check CHECK (length(trim(feature_key)) BETWEEN 1 AND 100),
    CONSTRAINT fk_site_snapshot_content_drafts_snapshot FOREIGN KEY (snapshot_id)
        REFERENCES site_snapshots(id) ON DELETE RESTRICT,
    CONSTRAINT fk_site_snapshot_content_drafts_updater FOREIGN KEY (updated_by)
        REFERENCES access_tokens(id) ON DELETE SET NULL
);

CREATE INDEX idx_site_snapshot_content_drafts_updated
    ON site_snapshot_content_drafts(updated_at DESC);
