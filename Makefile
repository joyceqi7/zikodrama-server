.PHONY: dev config test test-governance integration-up integration-down test-integration test-integration-local test-integration-mysql vet verify build

export GOPRIVATE ?= gitee.com/linpbin/*
export GONOSUMDB ?= gitee.com/linpbin/*
CONF ?= configs/config.yaml

dev:
	go run ./cmd/server -conf "$(CONF)"

config:
	protoc --proto_path=./internal \
		--proto_path=$$(go list -m -f '{{.Dir}}' gitee.com/linpbin/styx) \
		--proto_path=$$(go list -m -f '{{.Dir}}' gitee.com/linpbin/styx)/third_party \
		--plugin=protoc-gen-go=$$(go env GOPATH)/bin/protoc-gen-go \
		--go_out=paths=source_relative:./internal internal/conf/conf.proto

test:
	go test ./...

test-governance:
	go test ./internal/server/... -count=1

integration-up:
	docker compose -f internal/testsupport/compose.integration.yaml up -d --wait

integration-down:
	docker compose -f internal/testsupport/compose.integration.yaml down --remove-orphans

test-integration:
	@$(MAKE) test-integration-mysql

test-integration-local:
	TEST_REDIS_ADDR=127.0.0.1:36379 TEST_MYSQL_DSN='root:drama_local_test@tcp(127.0.0.1:33306)/?parseTime=true&loc=UTC&multiStatements=true' \
	$(MAKE) test-integration

test-integration-mysql:
	@test -n "$$TEST_MYSQL_DSN" || (echo "TEST_MYSQL_DSN is required" && exit 1)
	# 自动集成入口覆盖全部现行用例；浏览器常驻服务和百万用户压力测试仍需单独显式运行。
	go test ./internal/data/persistence/mysqlstore -run Integration -skip 'TestIAABrowserIntegration|TestConsumerUserScaleIntegration' -count=1 -v
	go test ./internal/data/redisstore -run Integration -count=1 -v
	go test ./internal/data -run Integration -count=1 -v

vet:
	go vet ./...

verify: test vet build

build:
	mkdir -p bin
	go build -o bin/drama-server ./cmd/server
