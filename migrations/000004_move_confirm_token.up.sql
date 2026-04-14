-- Move confirm_token and confirm_token_expires_at from subscriptions to confirmation_notifications,
-- and rename the table to subscription_confirmations.

ALTER TABLE confirmation_notifications
    ADD COLUMN confirm_token            TEXT        NOT NULL DEFAULT '',
    ADD COLUMN confirm_token_expires_at TIMESTAMPTZ;

-- Backfill tokens from subscriptions (for any existing pending rows)
UPDATE confirmation_notifications cn
SET confirm_token            = s.confirm_token,
    confirm_token_expires_at = s.confirm_token_expires_at
FROM subscriptions s
WHERE cn.subscription_id = s.id
  AND s.confirm_token IS NOT NULL
  AND cn.sent_at IS NULL;

-- Remove default now that backfill is done
ALTER TABLE confirmation_notifications
    ALTER COLUMN confirm_token DROP DEFAULT;

-- Unique index for fast token lookup
CREATE UNIQUE INDEX idx_subscription_confirmations_token
    ON confirmation_notifications (confirm_token)
    WHERE sent_at IS NULL;

-- Drop confirm_token columns from subscriptions
ALTER TABLE subscriptions
    DROP CONSTRAINT IF EXISTS subscriptions_confirm_state_check,
    DROP COLUMN confirm_token,
    DROP COLUMN confirm_token_expires_at;

DROP INDEX IF EXISTS idx_subscriptions_confirm_token;

-- Rename to a cleaner domain name
ALTER TABLE confirmation_notifications RENAME TO subscription_confirmations;

-- Rename the pending partial index
ALTER INDEX idx_confirmation_notifications_pending RENAME TO idx_subscription_confirmations_pending;
