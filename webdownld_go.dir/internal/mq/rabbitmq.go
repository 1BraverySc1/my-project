package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const exchangeName = "cloudraft.events"

type OrderEvent struct {
	OrderID    int64  `json:"order_id"`
	UserID     int64  `json:"user_id"`
	PlanID     int64  `json:"plan_id"`
	AmountCent int64  `json:"amount_cent"`
	TradeNo    string `json:"alipay_trade_no"`
}

const (
	RoutingKeyOrderCreated  = "order.created"
	RoutingKeyOrderPaid     = "order.paid"
	RoutingKeyOrderExpired  = "order.expired"
	RoutingKeyMemberUpgrade = "member.upgraded"
)

type Subscriber func(event []byte) error

// RabbitMQ 封装真实 RabbitMQ Topic Exchange、发布通道和消费者生命周期。
type RabbitMQ struct {
	conn      *amqp.Connection
	publishCh *amqp.Channel
	confirms  <-chan amqp.Confirmation
	mu        sync.Mutex
	wg        sync.WaitGroup
}

func NewRabbitMQ(rawURL string) (*RabbitMQ, error) {
	conn, err := amqp.Dial(rawURL)
	if err != nil {
		return nil, fmt.Errorf("connect rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open rabbitmq publish channel: %w", err)
	}
	if err := ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare rabbitmq exchange: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("enable rabbitmq publisher confirms: %w", err)
	}
	return &RabbitMQ{conn: conn, publishCh: ch, confirms: ch.NotifyPublish(make(chan amqp.Confirmation, 1))}, nil
}

func (r *RabbitMQ) Publish(routingKey string, event OrderEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r.mu.Lock()
	defer r.mu.Unlock()
	err = r.publishCh.PublishWithContext(ctx, exchangeName, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         data,
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", routingKey, err)
	}
	select {
	case confirmation := <-r.confirms:
		if !confirmation.Ack {
			return fmt.Errorf("rabbitmq rejected %s", routingKey)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait rabbitmq confirm for %s: %w", routingKey, ctx.Err())
	}
}

func (r *RabbitMQ) Subscribe(queueName, bindingKey string, sub Subscriber) error {
	ch, err := r.conn.Channel()
	if err != nil {
		return fmt.Errorf("open consumer channel: %w", err)
	}
	if err := ch.Qos(16, 0, false); err != nil {
		_ = ch.Close()
		return fmt.Errorf("set consumer qos: %w", err)
	}
	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return fmt.Errorf("declare queue %s: %w", queueName, err)
	}
	if err := ch.QueueBind(q.Name, bindingKey, exchangeName, false, nil); err != nil {
		_ = ch.Close()
		return fmt.Errorf("bind queue %s: %w", queueName, err)
	}
	deliveries, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return fmt.Errorf("consume queue %s: %w", queueName, err)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer ch.Close()
		for delivery := range deliveries {
			if err := sub(delivery.Body); err != nil {
				slog.Error("RabbitMQ event handler failed", "queue", queueName, "routing_key", delivery.RoutingKey, "error", err)
				_ = delivery.Nack(false, true)
				continue
			}
			_ = delivery.Ack(false)
		}
	}()
	return nil
}

func (r *RabbitMQ) InitSubscriptions(membershipCallback Subscriber) error {
	if err := r.Subscribe("cloudraft.order.audit", "order.#", func(event []byte) error {
		var evt OrderEvent
		if err := json.Unmarshal(event, &evt); err != nil {
			return err
		}
		slog.Info("order event", "order_id", evt.OrderID, "user_id", evt.UserID, "amount_cent", evt.AmountCent)
		return nil
	}); err != nil {
		return err
	}
	if err := r.Subscribe("cloudraft.order.paid", RoutingKeyOrderPaid, membershipCallback); err != nil {
		return err
	}
	return r.Subscribe("cloudraft.member.audit", "member.#", func(event []byte) error {
		var evt OrderEvent
		if err := json.Unmarshal(event, &evt); err != nil {
			return err
		}
		slog.Info("member event", "user_id", evt.UserID, "plan_id", evt.PlanID)
		return nil
	})
}

func (r *RabbitMQ) Shutdown() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.publishCh != nil {
		_ = r.publishCh.Close()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}
