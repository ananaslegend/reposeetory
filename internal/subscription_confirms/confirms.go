package subscription_confirms

//go:generate mockgen -source=subscriptionconfirms.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// PendingConfirmation is one outbox row joined with subscription + repository data.
type PendingConfirmation struct {
	ID           int64
	Email        string
	ConfirmToken string
	RepoOwner    string
	RepoName     string
}

// Repository is the storage contract for subscriptionconfirms.
type Repository interface {
	// Create inserts a new subscription_confirmations row.
	// Called within the same transaction as subscription creation.
	Create(ctx context.Context, p domain.CreateConfirmationParams) error

	// GetConfirmationsWithLock selects up to limit pending rows FOR UPDATE SKIP LOCKED.
	GetConfirmationsWithLock(ctx context.Context, limit int) ([]PendingConfirmation, error)

	// MarkSent sets sent_at = NOW() for the given notification row.
	MarkSent(ctx context.Context, id int64) error

	// GetByToken returns the pending confirmation record for the given token.
	// Returns domain.ErrTokenNotFound if no matching pending row exists.
	GetByToken(ctx context.Context, token string) (*ConfirmRecord, error)

	// MarkConfirmed atomically marks the notification sent and the subscription confirmed.
	MarkConfirmed(ctx context.Context, notifID int64, subscriptionID int64) error
}

// ConfirmRecord holds data needed to validate and complete a confirmation.
type ConfirmRecord struct {
	NotifID        int64
	SubscriptionID int64
	ExpiresAt      *time.Time
}

// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

// Config holds Confirmer dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
	Registry *prometheus.Registry
}

// Confirmer periodically drains the subscription_confirmations outbox and handles confirm requests.
type Confirmer struct {
	tx       transactor.Transactor
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
	m        confirmerMetrics
}

const confirmLimit = 1

// New creates a Confirmer from cfg.
func New(cfg Config) *Confirmer {
	return &Confirmer{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		mailer:   cfg.Mailer,
		interval: cfg.Interval,
		baseURL:  cfg.BaseURL,
		m:        newConfirmerMetrics(cfg.Registry),
	}
}

// Run blocks until ctx is cancelled, flushing the outbox on each interval.
func (c *Confirmer) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Flush(ctx)
		}
	}
}

// Flush drains all currently pending confirmations. Exported for testing.
func (c *Confirmer) Flush(ctx context.Context) {
	for {
		var processed bool
		err := c.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			items, err := c.repo.GetConfirmationsWithLock(ctx, confirmLimit)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return nil
			}
			p := items[0]
			err = c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
				To:           p.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + p.ConfirmToken,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
			})
			if err != nil {
				c.m.emailsSent.WithLabelValues("error").Inc()
				return err
			}
			c.m.emailsSent.WithLabelValues("ok").Inc()
			if err = c.repo.MarkSent(ctx, p.ID); err != nil {
				return err
			}
			processed = true
			return nil
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("subscriptionconfirms: flush failed")
			return
		}
		if !processed {
			return
		}
	}
}

// Confirm validates the token and confirms the subscription.
func (c *Confirmer) Confirm(ctx context.Context, token string) error {
	return c.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		rec, err := c.repo.GetByToken(ctx, token)
		if err != nil {
			return err
		}

		if rec.ExpiresAt != nil && rec.ExpiresAt.Before(time.Now()) {
			return domain.ErrTokenExpired
		}

		return c.repo.MarkConfirmed(ctx, rec.NotifID, rec.SubscriptionID)
	})
}
