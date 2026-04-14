-- Revert rename and restore confirm_token columns to subscriptions

ALTER TABLE subscription_confirmations RENAME TO confirmation_notifications;
ALTER INDEX idx_subscription_confirmations_pending RENAME TO idx_confirmation_notifications_pending;

ALTER TABLE subscriptions
    ADD COLUMN confirm_token            TEXT,
    ADD COLUMN confirm_token_expires_at TIMESTAMPTZ;

-- Backfill from confirmation_notifications
UPDATE subscriptions s
SET confirm_token            = cn.confirm_token,
    confirm_token_expires_at = cn.confirm_token_expires_at
FROM confirmation_notifications cn
WHERE cn.subscription_id = s.id
  AND cn.sent_at IS NULL;

CREATE UNIQUE INDEX idx_subscriptions_confirm_token
    ON subscriptions (confirm_token)
    WHERE confirm_token IS NOT NULL;

ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_confirm_state_check
        CHECK ((confirm_token IS NULL) = (confirmed_at IS NOT NULL));

DROP INDEX IF EXISTS idx_subscription_confirmations_token;

ALTER TABLE confirmation_notifications
    DROP COLUMN confirm_token,
    DROP COLUMN confirm_token_expires_at;
