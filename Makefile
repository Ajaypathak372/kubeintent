IMAGE ?= ghcr.io/ajaypathak/kubeintent:latest

run:
	go run ./main.go

build: bin docker-build

bin:
	go build ./...

docker-build:
	docker build -t $(IMAGE) .

tidy:
	go mod tidy
