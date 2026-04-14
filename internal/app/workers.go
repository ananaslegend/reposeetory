package app

import (
	"context"
	"sync"

	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/notifier"
	notifierrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
	subscription_confirms "github.com/ananaslegend/reposeetory/internal/subscription_confirms"
	screpo "github.com/ananaslegend/reposeetory/internal/subscription_confirms/repository"
)

func runWorkers(
	ctx context.Context,
	wg *sync.WaitGroup,
	cfg config.Config,
	txr transactor.Transactor,
	pool *pgxpool.Pool,
	mail emailer.Emailer,
	releases scanner.ReleaseProvider,
	reg *prometheus.Registry,
) {
	scan := scanner.New(scanner.Config{
		Tx:       txr,
		Repo:     scannerrepo.New(pool),
		GitHub:   releases,
		Interval: cfg.ScannerInterval,
		Registry: reg,
	})
	wg.Add(1)
	go func() { defer wg.Done(); scan.Run(ctx) }()

	notify := notifier.New(notifier.Config{
		Tx:       txr,
		Repo:     notifierrepo.New(pool),
		Mailer:   mail,
		Interval: cfg.NotifierInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})
	wg.Add(1)
	go func() { defer wg.Done(); notify.Run(ctx) }()

	confirm := subscription_confirms.New(subscription_confirms.Config{
		Tx:       txr,
		Repo:     screpo.New(pool),
		Mailer:   mail,
		Interval: cfg.ConfirmerInterval,
		BaseURL:  cfg.AppBaseURL,
		Registry: reg,
	})
	wg.Add(1)
	go func() { defer wg.Done(); confirm.Run(ctx) }()
}
