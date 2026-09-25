.PHONY: build test run doctor fmt
build:
	mkdir -p bin
	go build -o bin/cenarius ./cmd/cenarius
run: build
	./bin/cenarius serve
doctor: build
	./bin/cenarius doctor
test:
	go test ./...
fmt:
	gofmt -w cmd internal
smoke:
	./scripts/smoke-test.sh
