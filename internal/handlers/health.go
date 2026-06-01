package handlers

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DBPinger interface {
	Ping(ctx context.Context) error
}

type BrokerPinger interface {
	Ping(ctx context.Context) error
}

type StoragePinger interface {
	Ping(ctx context.Context) error
}

type Health struct {
	DB      DBPinger
	Broker  BrokerPinger
	Storage StoragePinger
}

func NewHealth(pool *pgxpool.Pool, broker BrokerPinger, storage StoragePinger) *Health {
	return &Health{
		DB:      pgxPinger{pool: pool},
		Broker:  broker,
		Storage: storage,
	}
}

func (h *Health) Check(ctx context.Context) HealthReport {
	comps := map[string]string{}
	status := "ok"

	if h.DB != nil {
		if err := h.DB.Ping(ctx); err != nil {
			comps["postgres"] = "error: " + err.Error()
			status = "degraded"
		} else {
			comps["postgres"] = "ok"
		}
	}
	if h.Broker != nil {
		if err := h.Broker.Ping(ctx); err != nil {
			comps["broker"] = "error: " + err.Error()
			status = "degraded"
		} else {
			comps["broker"] = "ok"
		}
	}
	if h.Storage != nil {
		if err := h.Storage.Ping(ctx); err != nil {
			comps["storage"] = "error: " + err.Error()
			status = "degraded"
		} else {
			comps["storage"] = "ok"
		}
	}
	return HealthReport{Status: status, Components: comps}
}

type pgxPinger struct {
	pool *pgxpool.Pool
}

func (p pgxPinger) Ping(ctx context.Context) error {
	if p.pool == nil {
		return nil
	}
	return p.pool.Ping(ctx)
}
