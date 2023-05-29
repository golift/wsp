.PHONY: build server client

build: server client

server:
	go build ./cmd/wsp_server

client:
	go build ./cmd/wsp_client
