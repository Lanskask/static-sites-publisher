.DEFAULT_GOAL := help
.PHONY: help run test build deploy

# The Oracle VM (k3s) that serves https://nex-sites-admin.duckdns.org; infra lives in the OracleCloud repo.
VPS_HOST ?= k3s-oracle

help: ## list the commands
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

run: ## run the admin locally at http://localhost:8080 with a fake login (dev:me)
	DEV_IDENTITY=dev:me ALLOWLIST=dev:me SITES_DIR=./data go run .

test: ## go vet, golangci-lint and go test -race
	go vet ./... && golangci-lint run && go test -race ./...

build: ## cross-compile for the VM (linux/arm64, static)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o sites-admin .

deploy: build ## copy the binary to the VM and restart its pod
	rsync -az sites-admin ubuntu@$(VPS_HOST):/opt/sites-admin/
	ssh ubuntu@$(VPS_HOST) sudo k3s kubectl rollout restart deploy/sites-admin
