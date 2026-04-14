package subscription_confirms_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	txmocks "github.com/ananaslegend/reposeetory/pkg/transactor/mocks"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription_confirms"
	"github.com/ananaslegend/reposeetory/internal/subscription_confirms/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newConfirmer(t *testing.T) (*subscription_confirms.Confirmer, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	c := subscription_confirms.New(subscription_confirms.Config{Tx: tx, Repo: repo, Mailer: m, BaseURL: "http://localhost:8080"})
	return c, tx, repo, m
}

// invokeWithinTransaction makes the mock call fn with the given context.
func invokeWithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

var testPending = subscription_confirms.PendingConfirmation{
	ID:           1,
	Email:        "user@example.com",
	ConfirmToken: "tok-abc123",
	RepoOwner:    "golang",
	RepoName:     "go",
}

// --- Flush tests ---

func TestConfirmer_FlushEmpty_NoMailer(t *testing.T) {
	c, tx, repo, _ := newConfirmer(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushOne_MailerCalled(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]subscription_confirms.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), domain.SendConfirmationParams{
		To:           "user@example.com",
		ConfirmURL:   "http://localhost:8080/api/confirm/tok-abc123",
		RepoFullName: "golang/go",
	}).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), int64(1)).Return(nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMailerError_NoMarkSentAndStops(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	smtpErr := errors.New("smtp timeout")
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]subscription_confirms.PendingConfirmation{testPending}, nil)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(smtpErr)
	// MarkSent must NOT be called on mailer error

	c.Flush(context.Background())
}

func TestConfirmer_FlushMultiple_ProcessedInOrder(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	second := subscription_confirms.PendingConfirmation{ID: 2, Email: "b@example.com", ConfirmToken: "tok-xyz", RepoOwner: "foo", RepoName: "bar"}

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]subscription_confirms.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]subscription_confirms.PendingConfirmation{second}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	c.Flush(context.Background())
}

// --- Confirm tests ---

func TestConfirm_HappyPath(t *testing.T) {
	c, tx, repo, _ := newConfirmer(t)

	exp := time.Now().Add(time.Hour)
	rec := &subscription_confirms.ConfirmRecord{NotifID: 1, SubscriptionID: 42, ExpiresAt: &exp}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetByToken(gomock.Any(), "validtoken").Return(rec, nil)
	repo.EXPECT().MarkConfirmed(gomock.Any(), int64(1), int64(42)).Return(nil)

	err := c.Confirm(context.Background(), "validtoken")
	require.NoError(t, err)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	c, tx, repo, _ := newConfirmer(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetByToken(gomock.Any(), "nosuchtoken").Return(nil, domain.ErrTokenNotFound)

	err := c.Confirm(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

func TestConfirm_TokenExpired(t *testing.T) {
	c, tx, repo, _ := newConfirmer(t)

	past := time.Now().Add(-time.Hour)
	rec := &subscription_confirms.ConfirmRecord{NotifID: 1, SubscriptionID: 42, ExpiresAt: &past}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetByToken(gomock.Any(), "expiredtoken").Return(rec, nil)

	err := c.Confirm(context.Background(), "expiredtoken")
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

// --- Metrics ---

func newConfirmerWithRegistry(t *testing.T) (*subscription_confirms.Confirmer, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	reg := prometheus.NewRegistry()
	c := subscription_confirms.New(subscription_confirms.Config{Tx: tx, Repo: repo, Mailer: m, BaseURL: "http://localhost:8080", Registry: reg})
	return c, tx, repo, m, reg
}

func TestConfirmer_Flush_IncrementsEmailSentMetric(t *testing.T) {
	c, tx, repo, m, reg := newConfirmerWithRegistry(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]subscription_confirms.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil)

	c.Flush(context.Background())

	expected := strings.NewReader(`
		# HELP confirmer_emails_sent_total Total number of confirmation emails attempted.
		# TYPE confirmer_emails_sent_total counter
		confirmer_emails_sent_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "confirmer_emails_sent_total"))
}
