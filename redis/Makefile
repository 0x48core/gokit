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

# Runs the tests against a throwaway single-node Redis.
test-integration:
	@docker run -d --rm --name gokit-test-redis -p 36379:6379 redis:8-alpine >/dev/null
	@until docker exec gokit-test-redis redis-cli ping >/dev/null 2>&1; do sleep 1; done
	@REDIS_ADDR='127.0.0.1:36379' go test -race -count=1 ./... ; \
		status=$$?; docker rm -f gokit-test-redis >/dev/null; exit $$status
