package kafka

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"
)

// explicitPartition marks a record whose partition the script chose.
type explicitPartition struct{}

// partitioner uses the script's partition when it set one; otherwise the
// key decides (murmur2, as the Java client does), and records without a
// key are spread (franz-go's sticky key partitioner).
type partitioner struct{ fallback kgo.Partitioner }

func newPartitioner() kgo.Partitioner {
	return partitioner{fallback: kgo.StickyKeyPartitioner(nil)}
}

func (p partitioner) ForTopic(topic string) kgo.TopicPartitioner {
	return &topicPartitioner{fallback: p.fallback.ForTopic(topic)}
}

type topicPartitioner struct{ fallback kgo.TopicPartitioner }

func explicit(r *kgo.Record) bool {
	return r.Context != nil && r.Context.Value(explicitPartition{}) != nil
}

func (t *topicPartitioner) RequiresConsistency(r *kgo.Record) bool {
	return explicit(r) || t.fallback.RequiresConsistency(r)
}

func (t *topicPartitioner) Partition(r *kgo.Record, n int) int {
	if explicit(r) {
		return int(r.Partition)
	}
	return t.fallback.Partition(r, n)
}

// OnNewBatch passes batch boundaries on, so the sticky partitioner moves
// on as franz-go expects.
func (t *topicPartitioner) OnNewBatch() {
	if b, ok := t.fallback.(kgo.TopicPartitionerOnNewBatch); ok {
		b.OnNewBatch()
	}
}

// withPartition returns ctx marked with an explicit partition.
func withPartition(ctx context.Context) context.Context {
	return context.WithValue(ctx, explicitPartition{}, true)
}
