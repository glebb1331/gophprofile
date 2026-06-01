package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/gophprofile/avatars-service/internal/config"
)

type Client struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	cfg     config.BrokerConfig
}

func New(ctx context.Context, cfg config.BrokerConfig) (*Client, error) {
	var (
		conn *amqp.Connection
		err  error
	)
	for attempt := 0; attempt < cfg.RetryAttempts; attempt++ {
		conn, err = amqp.Dial(cfg.URL)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff(cfg.RetryBaseDelay, attempt)):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	c := &Client{conn: conn, channel: ch, cfg: cfg}
	if err := c.declare(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := ch.Qos(cfg.PrefetchCount, 0, false); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}
	return c, nil
}

func (c *Client) declare() error {
	if err := c.channel.ExchangeDeclare(c.cfg.Exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange: %w", err)
	}
	for _, q := range []struct {
		name       string
		routingKey string
	}{
		{c.cfg.UploadQueue, c.cfg.UploadRouting},
		{c.cfg.DeleteQueue, c.cfg.DeleteRouting},
	} {
		if _, err := c.channel.QueueDeclare(q.name, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %q: %w", q.name, err)
		}
		if err := c.channel.QueueBind(q.name, q.routingKey, c.cfg.Exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %q: %w", q.name, err)
		}
	}
	return nil
}

func (c *Client) Publish(ctx context.Context, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	return c.channel.PublishWithContext(ctx, c.cfg.Exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Body:         body,
	})
}

func (c *Client) Consume(queue string) (<-chan amqp.Delivery, error) {
	return c.channel.Consume(queue, c.cfg.ConsumerTag+"-"+queue, false, false, false, false, nil)
}

func (c *Client) Channel() *amqp.Channel {
	return c.channel
}

func (c *Client) Ping(ctx context.Context) error {
	if c.conn == nil || c.conn.IsClosed() {
		return fmt.Errorf("connection closed")
	}
	return nil
}

func (c *Client) Close() error {
	if c.channel != nil {
		_ = c.channel.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
