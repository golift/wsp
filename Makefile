.PHONY: build server client

build: server client

server:
	go build -race ./cmd/wsp_server

client:
	go build -race ./cmd/wsp_client
