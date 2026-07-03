#!/bin/bash
# Mock grpc_cli for TPU-CC GetPartitionState
# Reads the expected partition from /tmp/expected_partition and reports it as STATE_AVAILABLE

PARTITION=$(cat /tmp/expected_partition 2>/dev/null || echo "default-partition")

echo "partition_name: \"${PARTITION}\""
echo "state: STATE_AVAILABLE"
