.PHONY: generate generate-check fmt-check test race vet build verify

PROTO_FILES := $(wildcard api/config/v1/*.proto)
GENERATED_FILES := $(PROTO_FILES:.proto=.pb.go)

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc -I . -I third_party --go_out=. --go_opt=paths=source_relative $(PROTO_FILES)

generate-check:
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc -I . -I third_party --go_out="$$tmp" --go_opt=paths=source_relative $(PROTO_FILES); \
	for file in $(GENERATED_FILES); do diff -u "$$file" "$$tmp/$$file"; done

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

verify: generate-check fmt-check vet test build
