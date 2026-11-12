.PHONY: run build build-arm64 build-amd64 test fmt vet

run:
	go run ./cmd/multiparty

build:
	CGO_ENABLED=0 go build -o bin/multiparty ./cmd/multiparty

# Binarios para el servidor de Oracle Cloud: Ampere A1 (ARM64) o E2.1.Micro (AMD64).
build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o bin/multiparty-linux-arm64 ./cmd/multiparty

build-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/multiparty-linux-amd64 ./cmd/multiparty

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
