#!/usr/bin/env bash
# Starts the single-node Kafka broker of docker-compose.yml, waits until it
# is healthy, and creates the topics the tests and examples use. Stop it
# with: docker compose -f testenv/kafka/docker-compose.yml down -v
set -euo pipefail
dir=$(cd "$(dirname "$0")" && pwd)
compose=(docker compose -f "$dir/docker-compose.yml")
"${compose[@]}" up -d --wait kafka
for topic in orders events loadtool-real; do
  "${compose[@]}" exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 \
    --create --if-not-exists --topic "$topic" --partitions 3 --replication-factor 1
done
echo "Kafka is ready on 127.0.0.1:9092 (topics: orders, events, loadtool-real)"
