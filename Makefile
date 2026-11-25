.DEFAULT_GOAL := help
.PHONY: help run test build deploy dns-check e2e

# arm64 for ARM servers (Oracle Ampere, AWS Graviton, Raspberry Pi)
GOARCH ?= amd64
# ssh target for deploy, e.g. root@203.0.113.7
HOST ?=

help: ## list the commands
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

run: ## run the admin locally at http://localhost:8080 with a fake login (dev:me)
	cd src && DEV_IDENTITY=dev:me ALLOWLIST=dev:me SITES_DIR=../data go run .

test: ## go vet, golangci-lint, go test -race, and the Python script tests
	cd src && go vet ./... && golangci-lint run && go test -race ./...
	python3 -m unittest discover -s scripts -p 'test_*.py'

build: ## static Linux binary at bin/sites-admin (GOARCH=amd64 or arm64), for servers without Docker
	cd src && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -o ../bin/sites-admin .

deploy: ## copy src/ and deploy/ to HOST over ssh and start them with docker compose
	python3 scripts/ssh_deploy.py $(HOST)

dns-check: ## check that the domains in deploy/.env resolve to one server
	python3 scripts/dns_check.py

e2e: ## run deploy/ on local Docker with certificates from Pebble (Let's Encrypt's test server); KEEP=1 leaves it up
	python3 scripts/e2e.py $(if $(KEEP),--keep)
