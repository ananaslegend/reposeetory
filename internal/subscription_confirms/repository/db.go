package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription_confirms"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements subscription_confirms.Repository using pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// New creates a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// conn returns the active transaction from ctx, or the pool if no transaction is present.
func (r *Repository) conn(ctx context.Context) transactor.Conn {
	return transactor.ConnFromContext(ctx, r.pool)
}

// Create inserts a new subscription_confirmations row with the given token.
func (r *Repository) Create(ctx context.Context, p domain.CreateConfirmationParams) error {
	_, err := r.conn(ctx).Exec(ctx, `
		INSERT INTO subscription_confirmations (subscription_id, confirm_token, confirm_token_expires_at)
		VALUES ($1, $2, $3)
	`, p.SubscriptionID, p.Token, p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("subscription_confirms: create: %w", err)
	}
	return nil
}

// GetConfirmationsWithLock selects up to limit rows pending email delivery FOR UPDATE SKIP LOCKED.
func (r *Repository) GetConfirmationsWithLock(ctx context.Context, limit int) ([]subscription_confirms.PendingConfirmation, error) {
	rows, err := r.conn(ctx).Query(ctx, `
		SELECT cn.id, s.email, cn.confirm_token, rep.owner, rep.name
		FROM subscription_confirmations cn
		JOIN subscriptions  s   ON cn.subscription_id = s.id
		JOIN repositories   rep ON s.repository_id    = rep.id
		WHERE cn.email_sent_at IS NULL
		ORDER BY cn.created_at
		LIMIT $1
		FOR UPDATE OF cn SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("subscription_confirms: get with lock: %w", err)
	}
	defer rows.Close()

	var items []subscription_confirms.PendingConfirmation
	for rows.Next() {
		var c subscription_confirms.PendingConfirmation
		if err := rows.Scan(&c.ID, &c.Email, &c.ConfirmToken, &c.RepoOwner, &c.RepoName); err != nil {
			return nil, fmt.Errorf("subscription_confirms: scan row: %w", err)
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// MarkSent sets email_sent_at = NOW() for the given row.
func (r *Repository) MarkSent(ctx context.Context, id int64) error {
	_, err := r.conn(ctx).Exec(ctx, `
		UPDATE subscription_confirmations SET email_sent_at = $1 WHERE id = $2
	`, time.Now(), id)
	if err != nil {
		return fmt.Errorf("subscription_confirms: mark sent %d: %w", id, err)
	}
	return nil
}

// GetByToken returns the pending confirmation record for the given token.
// Looks for rows where confirmed_at IS NULL (user has not yet confirmed).
func (r *Repository) GetByToken(ctx context.Context, token string) (*subscription_confirms.ConfirmRecord, error) {
	var rec subscription_confirms.ConfirmRecord
	err := r.conn(ctx).QueryRow(ctx, `
		SELECT id, subscription_id, confirm_token_expires_at
		FROM subscription_confirmations
		WHERE confirm_token = $1 AND confirmed_at IS NULL
	`, token).Scan(&rec.NotifID, &rec.SubscriptionID, &rec.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTokenNotFound
		}
		return nil, fmt.Errorf("subscription_confirms: get by token: %w", err)
	}
	return &rec, nil
}

// MarkConfirmed sets confirmed_at = NOW() for the given subscription_confirmations row.
// The subscription's confirmed state is now derived from this table — no subscriptions update needed.
func (r *Repository) MarkConfirmed(ctx context.Context, notifID int64, _ int64) error {
	_, err := r.conn(ctx).Exec(ctx, `
		UPDATE subscription_confirmations SET confirmed_at = $1 WHERE id = $2
	`, time.Now(), notifID)
	if err != nil {
		return fmt.Errorf("subscription_confirms: mark confirmed %d: %w", notifID, err)
	}
	return nil
}
