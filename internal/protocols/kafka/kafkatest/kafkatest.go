// Package kafkatest starts an in-process Kafka cluster (franz-go's kfake)
// for the Kafka module's tests and the demo API (ADR-022), so neither
// needs Docker. Real-broker tests use testenv/kafka/docker-compose.yml.
package kafkatest

import (
	"github.com/twmb/franz-go/pkg/kfake"
)

// Partitions is how many partitions each seeded topic has.
const Partitions = 3

// NewCluster starts a one-broker cluster with the given topics, each with
// Partitions partitions. port 0 picks a free port. Topics are not created
// automatically: producing to another topic fails, as on a broker with
// auto-creation off.
func NewCluster(port int, topics ...string) (*kfake.Cluster, error) {
	opts := []kfake.Opt{kfake.NumBrokers(1), kfake.SeedTopics(Partitions, topics...)}
	if port != 0 {
		opts = append(opts, kfake.Ports(port))
	}
	return kfake.NewCluster(opts...)
}
