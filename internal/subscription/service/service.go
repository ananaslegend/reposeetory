package service

//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// normalizeRepo extracts "owner/name" from a URL or a plain "owner/name" string.
// Strips trailing ".git" and takes the last two slash-separated segments.
func normalizeRepo(s string) string {
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return s
}

// Repository is the storage contract expected by this service.
type Repository interface {
	UpsertRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error)
	CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)
	DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error)
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)
}

// ConfirmationCreator creates a pending confirmation notification row.
// Called within the same transaction as CreateSubscription.
type ConfirmationCreator interface {
	Create(ctx context.Context, p domain.CreateConfirmationParams) error
}

// RemoteRepositoryProvider checks whether a GitHub repository exists.
type RemoteRepositoryProvider interface {
	RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error)
}

// Config holds all dependencies and settings for Service.
type Config struct {
	Tx              transactor.Transactor
	Repo            Repository
	Confirms        ConfirmationCreator
	GitHub          RemoteRepositoryProvider
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
	Registry        *prometheus.Registry
}

type Service struct {
	tx              transactor.Transactor
	repo            Repository
	confirms        ConfirmationCreator
	github          RemoteRepositoryProvider
	appBaseURL      string
	confirmTokenTTL time.Duration
	m               serviceMetrics
}

func New(cfg Config) *Service {
	return &Service{
		tx:              cfg.Tx,
		repo:            cfg.Repo,
		confirms:        cfg.Confirms,
		github:          cfg.GitHub,
		appBaseURL:      cfg.AppBaseURL,
		confirmTokenTTL: cfg.ConfirmTokenTTL,
		m:               newServiceMetrics(cfg.Registry),
	}
}

func (s *Service) Subscribe(ctx context.Context, p domain.SubscribeParams) error {
	p.Repository = normalizeRepo(p.Repository)
	if !repoNameRe.MatchString(p.Repository) {
		return domain.ErrInvalidRepoFormat
	}
	parts := strings.SplitN(p.Repository, "/", 2)
	owner, name := parts[0], parts[1]

	exists, err := s.github.RepoExists(ctx, domain.RepoExistsParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("check repo existence: %w", err)
	}
	if !exists {
		return domain.ErrRepoNotFound
	}

	repoID, err := s.repo.UpsertRepo(ctx, domain.UpsertRepoParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("upsert repo: %w", err)
	}

	confirmToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate confirm token: %w", err)
	}
	unsubscribeToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate unsubscribe token: %w", err)
	}

	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		sub, err := s.repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
			Email:            p.Email,
			RepositoryID:     repoID,
			UnsubscribeToken: unsubscribeToken,
		})
		if err != nil {
			return err // ErrAlreadyExists propagated as-is
		}

		return s.confirms.Create(ctx, domain.CreateConfirmationParams{
			SubscriptionID: sub.ID,
			Token:          confirmToken,
			ExpiresAt:      time.Now().Add(s.confirmTokenTTL),
		})
	})
	if err != nil {
		return err
	}

	zerolog.Ctx(ctx).Info().
		Str("email", p.Email).
		Str("repo", p.Repository).
		Int64("repo_id", repoID).
		Msg("subscription created")
	s.m.subscriptionsCreated.Inc()
	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	deleted, err := s.repo.DeleteByUnsubscribeToken(ctx, token)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if !deleted {
		return domain.ErrTokenNotFound
	}

	zerolog.Ctx(ctx).Info().Msg("unsubscribed")
	s.m.subscriptionsDeleted.Inc()
	return nil
}

func (s *Service) ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
	subs, err := s.repo.ListByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("list by email: %w", err)
	}
	return subs, nil
}
