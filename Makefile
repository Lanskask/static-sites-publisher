.DEFAULT_GOAL := help
.PHONY: help run test build deploy dns-check e2e tf-plan tf-apply tf-plan-apply tf-destroy

# arm64 for ARM servers (Oracle Ampere, AWS Graviton, Raspberry Pi)
GOARCH ?= amd64
# ssh target for deploy, e.g. root@203.0.113.7
HOST ?=
# GCP project for the throwaway test VM in deploy/e2e/gcp
GCP_PROJECT ?=
TF = terraform -chdir=deploy/e2e/gcp

help: ## list the commands
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-13s %s\n", $$1, $$2}'

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

deploy/e2e/gcp/.terraform:
	$(TF) init

tf-plan: deploy/e2e/gcp/.terraform ## show what the GCP test VM would create (GCP_PROJECT=...); saves the plan
	@test -n "$(GCP_PROJECT)" || { echo "set GCP_PROJECT=<your project id>"; exit 1; }
	$(TF) plan -var project=$(GCP_PROJECT) -out=tfplan

tf-apply: ## ask, then create the GCP test VM from the plan tf-plan saved
	@test -f deploy/e2e/gcp/tfplan || { echo "no saved plan: run make tf-plan first"; exit 1; }
	@read -p "Create these resources? It costs money unless free tier. [y/N] " a && [ "$$a" = y ]
	$(TF) apply tfplan && rm deploy/e2e/gcp/tfplan

tf-plan-apply: tf-plan tf-apply ## tf-plan then tf-apply

tf-destroy: deploy/e2e/gcp/.terraform ## delete the GCP test VM, network and firewall (asks first)
	@test -n "$(GCP_PROJECT)" || { echo "set GCP_PROJECT=<your project id>"; exit 1; }
	$(TF) destroy -var project=$(GCP_PROJECT)
