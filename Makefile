.PHONY: build server client test

build: server client

server:
	go build ./cmd/wsp_server

client:
	go build ./cmd/wsp_client

test:
	go run ./examples/test_api/main.go