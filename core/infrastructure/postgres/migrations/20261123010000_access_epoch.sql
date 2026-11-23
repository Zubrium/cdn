-- Internal monotonic authorization epoch. Every grants, groups or credentials
-- mutation increments it in the same transaction as the change and the cache
-- invalidation notification. The public credential-bound session contract is
-- unchanged.
ALTER TABLE access.users
  ADD COLUMN access_epoch bigint NOT NULL DEFAULT 1 CHECK (access_epoch > 0);
