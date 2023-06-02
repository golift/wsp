.PHONY: build server client mulery

build: server client

server:
	go build -race ./cmd/wsp_server

client:
	go build -race ./cmd/wsp_client

mulery:
	go build ./cmd/mulery