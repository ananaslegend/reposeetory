package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/jackc/pgx/v5/pgconn"
)

const uniqueViolationCode = "23505"

func (r *Repository) conn(ctx context.Context) transactor.Conn {
	return transactor.ConnFromContext(ctx, r.pool)
}

func (r *Repository) CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.conn(ctx).QueryRow(ctx, `
		INSERT INTO subscriptions (email, repository_id, unsubscribe_token)
		VALUES ($1, $2, $3)
		RETURNING id, email, repository_id, unsubscribe_token, created_at
	`, p.Email, p.RepositoryID, p.UnsubscribeToken).Scan(
		&sub.ID, &sub.Email, &sub.RepositoryID,
		&sub.UnsubscribeToken, &sub.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return nil, domain.ErrAlreadyExists
		}
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	return &sub, nil
}

func (r *Repository) DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM subscriptions WHERE unsubscribe_token = $1
	`, token)
	if err != nil {
		return false, fmt.Errorf("delete subscription: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repository) ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, r.owner, r.name, sc.confirmed_at, s.created_at
		FROM subscriptions s
		JOIN repositories r ON r.id = s.repository_id
		JOIN subscription_confirmations sc ON sc.subscription_id = s.id AND sc.confirmed_at IS NOT NULL
		WHERE s.email = $1
		ORDER BY s.created_at DESC
	`, email)
	if err != nil {
		return nil, fmt.Errorf("list by email: %w", err)
	}
	defer rows.Close()

	var result []domain.SubscriptionView
	for rows.Next() {
		var v domain.SubscriptionView
		if err := rows.Scan(&v.ID, &v.RepoOwner, &v.RepoName, &v.ConfirmedAt, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("list by email: scan: %w", err)
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list by email: rows: %w", err)
	}
	return result, nil
}
