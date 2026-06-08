BINARY := snagateway
PKG    := ./cmd/snagateway

.PHONY: build linux run fmt vet test clean

build:
	go build -o $(BINARY) $(PKG)

# The real target: the Linux gateway (LLC2/AF_LLC is Linux-only).
linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 $(PKG)

run: build
	./$(BINARY) run -config config.json

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64
