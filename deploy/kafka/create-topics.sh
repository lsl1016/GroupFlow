#!/usr/bin/env bash
set -e
BROKER=${KAFKA_BROKER:-kafka:9092}
KAFKA_TOPICS_BIN=${KAFKA_TOPICS_BIN:-/opt/kafka/bin/kafka-topics.sh}
create_topic() {
  local topic=$1
  local partitions=${2:-6}
  "$KAFKA_TOPICS_BIN" --bootstrap-server "$BROKER" --create --if-not-exists --topic "$topic" --partitions "$partitions" --replication-factor 1
}
# 一期主投递 Topic：群消息、撤回事件统一通过 eventType 区分，保证同群尽量落到同一分区。
create_topic group-message-topic 12
create_topic group-system-event-topic 6
create_topic group-mention-topic 6
create_topic group-message-recall-topic 6
create_topic group-audit-topic 6
create_topic group-message-dlq-topic 6
create_topic group-mention-dlq-topic 6
create_topic group-message-recall-dlq-topic 6
