package rabbitmq

import (
	"context"

	"github.com/gophprofile/avatars-service/internal/config"
	"github.com/gophprofile/avatars-service/internal/domain"
)

type Publisher struct {
	client *Client
	cfg    config.BrokerConfig
}

func NewPublisher(client *Client, cfg config.BrokerConfig) *Publisher {
	return &Publisher{client: client, cfg: cfg}
}

func (p *Publisher) PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) error {
	return p.client.Publish(ctx, p.cfg.UploadRouting, event)
}

func (p *Publisher) PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) error {
	return p.client.Publish(ctx, p.cfg.DeleteRouting, event)
}
