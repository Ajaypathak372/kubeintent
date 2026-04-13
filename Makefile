IMAGE ?= ghcr.io/ajaypathak372/kubeintent:latest

run:
	go run ./main.go

build: bin docker-build

bin:
	go build ./...

docker-build:
	docker build -t $(IMAGE) .

deploy:
	kubectl apply -f config/install.yaml

undeploy:
	kubectl delete -f config/install.yaml

tidy:
	go mod tidy

# ---------------------------------------------------------------------------
# Demo: one-command closed-loop demo on a local kind cluster
# ---------------------------------------------------------------------------
.PHONY: demo demo-clean

demo:
	@config/samples/demo/demo.sh

demo-clean:
	@config/samples/demo/demo-clean.sh
