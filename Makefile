.PHONY: run build build-arm64 test fmt vet

run:
	go run ./cmd/multiparty

build:
	CGO_ENABLED=0 go build -o bin/multiparty ./cmd/multiparty

# Binario para el servidor de Oracle Cloud (Ampere, ARM64).
build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/multiparty-linux-arm64 ./cmd/multiparty

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
