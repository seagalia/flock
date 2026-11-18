package multi

import (
	"context"
	"fmt"

	"github.com/ArthurHlt/emitter"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type Nats struct {
	entry        zerolog.Logger
	conn         *nats.Conn
	instanceUuid string
}

func NewNats(natsOptions *config.NatsMultiOptions) (*Nats, error) {
	entry := logging.Component("nats_multi")
	conn, err := nats.Connect(natsOptions.Url, natsOptions.ToOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %v", err)
	}
	entry.Info().Str("nats_url", natsOptions.Url).Msg("connected to nats")
	return &Nats{
		entry:        entry,
		conn:         conn,
		instanceUuid: uuid.Must(uuid.NewRandom()).String(),
	}, nil
}

func (n *Nats) reEmit(topic string) nats.MsgHandler {
	evType := observer.EventType(observer.EventType_value[topic])
	return func(msg *nats.Msg) {
		subject := &observer.Subject{}
		err := proto.Unmarshal(msg.Data, subject)
		if err != nil {
			n.entry.Error().Str("topic", topic).Msgf("failed to decode event: %v", err)
			return
		}
		metadata := subject.Metadata.AsMap()
		fNats, hasFNats := metadata["from_nats"]
		instUuid, hasUuid := metadata["instance_uuid"]
		if hasFNats && fNats.(bool) && hasUuid && instUuid.(string) == n.instanceUuid {
			return
		}
		if !hasFNats || !fNats.(bool) {
			metadata["from_nats"] = true
			metadata["instance_uuid"] = n.instanceUuid
		}
		subject.Metadata, _ = structpb.NewStruct(metadata) //nolint:errcheck

		n.entry.Debug().Str("topic", topic).Interface("subject", subject).Msg("re-emitting event to internal bus")
		observe.EmitSubject(evType, subject)
	}
}

func (n *Nats) Run(ctx context.Context) error {
	for _, topic := range observer.EventType_name {
		subjectName := n.transformTopic(topic)
		_, err := n.conn.Subscribe(subjectName, n.reEmit(topic))
		if err != nil {
			// a connection left open would keep the health check of the controller green with no event bridged
			n.conn.Close()
			return fmt.Errorf("failed to subscribe to %s: %w", topic, err)
		}
		n.entry.Info().Str("subject_name", topic).Msg("subscribed to subject in nats")
	}
	observe.OnAll(n)
	<-ctx.Done()
	// off the bus first, since a publish on a draining connection fails
	observe.OffAll(n)
	closed := n.conn.StatusChanged(nats.CLOSED)
	err := n.conn.Drain()
	if err != nil {
		n.entry.Warn().Err(err).Msg("could not drain the connection, closing it")
		n.conn.Close()
		return nil
	}
	<-closed
	return nil
}

func (n *Nats) Observe(e emitter.Event) {
	typedEvent, ok := e.(*emitter.EventOf[*observer.Subject])
	if !ok {
		return
	}
	// do not re-emit event from nats
	subject := typedEvent.TypedSubject()
	metadata := subject.Metadata.AsMap()
	if fNats, ok := metadata["from_nats"]; ok && fNats.(bool) {
		return
	}
	metadata["from_nats"] = true
	metadata["instance_uuid"] = n.instanceUuid
	subject.Metadata, _ = structpb.NewStruct(metadata) //nolint:errcheck

	subjectName := n.transformTopic(typedEvent.Topic())
	n.entry.Debug().Str("subject_name", subjectName).Interface("subject", subject).Msg("publishing event to nats")
	data, err := proto.Marshal(typedEvent.TypedSubject())
	if err != nil {
		n.entry.Error().Msgf("failed to encode event: %v", err)
		return
	}
	err = n.conn.Publish(subjectName, data)
	if err != nil {
		n.entry.Error().Msgf("failed to publish event: %v", err)
	}
}

func (n *Nats) transformTopic(topic string) string {
	return "controller." + topic
}

// Ping makes a round trip to the nats server, ctx must have a deadline
func (n *Nats) Ping(ctx context.Context) error {
	return n.conn.FlushWithContext(ctx)
}
