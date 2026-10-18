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

# Runs the MySQL, PostgreSQL and MongoDB registry tests against throwaway containers.
test-integration:
	@docker run -d --rm --name snowflake-test-mysql -e MYSQL_ROOT_PASSWORD=pass -e MYSQL_DATABASE=snowflake -p 33306:3306 mysql:8.4
	@docker run -d --rm --name snowflake-test-postgres -e POSTGRES_PASSWORD=pass -e POSTGRES_DB=snowflake -p 35432:5432 postgres:17
	@docker run -d --rm --name snowflake-test-mongo -p 37017:27017 mongo:7
	@until docker exec snowflake-test-mysql mysqladmin ping -h127.0.0.1 -ppass --silent 2>/dev/null; do sleep 1; done
	@until docker exec snowflake-test-postgres pg_isready -h 127.0.0.1 -q 2>/dev/null; do sleep 1; done
	@SNOWFLAKE_MYSQL_DSN='root:pass@tcp(127.0.0.1:33306)/snowflake' \
		SNOWFLAKE_POSTGRES_DSN='postgres://postgres:pass@127.0.0.1:35432/snowflake' \
		SNOWFLAKE_MONGODB_URI='mongodb://127.0.0.1:37017' \
		go test -race -count=1 ./... ; \
		status=$$?; docker rm -f snowflake-test-mysql snowflake-test-postgres snowflake-test-mongo >/dev/null; exit $$status
