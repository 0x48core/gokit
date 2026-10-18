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


test-up:
	@docker-compose \
		-f docker/docker-compose-test.yaml up \
		--force-recreate \
		--abort-on-container-exit \
		--exit-code-from app \
		--build


dev-up:
	@docker-compose -f docker/docker-compose-dev.yaml up \
	--force-recreate \
	--abort-on-container-exit