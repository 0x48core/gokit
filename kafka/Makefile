pwd = $(shell pwd)
src = `go list -f {{.Dir}} ./... | grep -v /vendor/`

ifeq ($(shell uname -m), arm64)
	tags = dynamic
else
	tags =
endif

fmt:
	@echo "Formatting..."
	@goimports -w $(src)
	@gofumpt -w $(src)
	@gci write -s standard -s default $(src)

lint:
	@echo "Lint checking..."
	@golangci-lint --version
	@golangci-lint -v --config .golangci.yaml run
	@go vet -tags=$(tags) $(src)

test:
	@echo "Testing..."
	@go test -v -vet=off -tags="$(TAGS)" `go list ./... | grep -v cmd` -race -p 1 --cover -coverprofile cover.out

# Runs the producer and consumer tests against a throwaway single-node Kafka broker.
test-integration:
	@docker run -d --rm --name gokit-test-kafka -p 39092:39092 \
		-e KAFKA_NODE_ID=1 \
		-e KAFKA_PROCESS_ROLES=broker,controller \
		-e KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:39092,CONTROLLER://localhost:29093 \
		-e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://127.0.0.1:39092 \
		-e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
		-e KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT \
		-e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
		-e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:29093 \
		-e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
		-e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
		-e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
		-e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
		-e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk \
		confluentinc/cp-kafka:8.0.0
	@until docker exec gokit-test-kafka kafka-broker-api-versions --bootstrap-server 127.0.0.1:39092 >/dev/null 2>&1; do sleep 1; done
	@KAFKA_BROKERS='127.0.0.1:39092' go test -race -count=1 ./... ; \
		status=$$?; docker rm -f gokit-test-kafka >/dev/null; exit $$status
