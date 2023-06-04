.PHONY: build mulery docker

build: mulery

mulery:
	go build -o mulery ./cmd/mulery

docker:
	docker build -t mulery -f ./cmd/mulery/Dockerfile .
