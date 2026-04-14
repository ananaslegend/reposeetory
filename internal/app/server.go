package app

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	"github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	subscription_confirms "github.com/ananaslegend/reposeetory/internal/subscription_confirms"
	screpo "github.com/ananaslegend/reposeetory/internal/subscription_confirms/repository"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

func newHTTPServer(cfg config.Config, pool *pgxpool.Pool, log zerolog.Logger, reg *prometheus.Registry) *http.Server {
	txr := transactor.New(pool)
	confirmsRepo := screpo.New(pool)

	svc := service.New(service.Config{
		Tx:              txr,
		Repo:            repository.New(pool),
		Confirms:        confirmsRepo,
		GitHub:          githubclient.NewClient(cfg.GitHubToken),
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        reg,
	})

	confirmer := subscription_confirms.New(subscription_confirms.Config{
		Tx:   txr,
		Repo: confirmsRepo,
	})

	subHandler := subhttp.NewHandler(svc, confirmer)
	router := httpapi.NewRouter(httpapi.RouterConfig{
		Log:        log,
		SubHandler: subHandler,
		Registry:   reg,
	})

	return &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
	}
}
