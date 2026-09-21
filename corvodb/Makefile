BINARY := bin/corvo
PACKAGES := ./...

.PHONY: build test race cover bench fuzz vet fmt lint demo clean

build:
	go build -o $(BINARY) ./cmd/corvo

test:
	go test -race $(PACKAGES)

cover:
	go test -coverprofile=coverage.out $(PACKAGES)
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -1

bench:
	go test -run XXX -bench . -benchtime 2000x $(PACKAGES)

fuzz:
	go test -run XXX -fuzz FuzzParse -fuzztime 30s ./internal/sql

vet:
	go vet $(PACKAGES)

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l . )" || (echo "arquivos fora do formato:"; gofmt -l .; exit 1)
	go vet $(PACKAGES)

demo: build
	@rm -f exemplos/loja.db exemplos/loja.db-log
	$(BINARY) -f exemplos/loja.sql exemplos/loja.db

clean:
	rm -rf bin coverage.out coverage.html
