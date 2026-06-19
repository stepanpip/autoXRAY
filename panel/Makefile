BIN=xray-panel

.PHONY: build test
build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BIN) .

test:
	go test ./...
