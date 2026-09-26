# Agritelem developer entry points. CI runs the same targets.
GOLANGCI_LINT_VERSION := v2.14.0
BACKEND := backend
DEV := .dev
DB_URL := postgres://agritelem:agritelem-dev-only@localhost:5432/agritelem?sslmode=disable
LOCATION := dk.herdnumber/123456
GATEWAY := ie.example.gateway
IOS := ios-client
IOS_DESTINATION ?= platform=iOS Simulator,name=iPhone 18 Pro
XCODEBUILD := xcodebuild -project $(IOS)/Agritelem.xcodeproj -scheme Agritelem -destination '$(IOS_DESTINATION)' \
	-derivedDataPath .build/ios -skipPackagePluginValidation

.PHONY: help contracts generate check-generated lint test test-integration dev-keys up down seed token sim ios-project ios-test-kit ios-test ios-build

help: ## List targets.
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

contracts: ## Rebuild contracts/openapi.json from the ICAR bundle and the Agritelem extension.
	cd $(BACKEND) && go run ./cmd/contractgen

generate: contracts ## Regenerate all generated code (contract, Go server, SQL).
	cd $(BACKEND) && go generate ./...

check-generated: generate ## Fail if generated files differ from what is committed.
	@changes="$$(git status --porcelain -- contracts $(BACKEND))"; \
	if [ -n "$$changes" ]; then echo "generated files are stale or untracked:"; echo "$$changes"; exit 1; fi

lint: ## Run golangci-lint.
	cd $(BACKEND) && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --build-tags integration ./...

test: ## Run unit tests with the race detector.
	cd $(BACKEND) && go test -race ./...

test-integration: ## Run integration tests (requires Docker).
	cd $(BACKEND) && go test -race -tags integration ./...

dev-keys: ## Create the local JWT signing key pair in .dev/ (once).
	cd $(BACKEND) && go run ./cmd/devtoken keys -dir ../$(DEV)

up: dev-keys ## Build and start the database and API (http://localhost:8080).
	docker compose up -d --build --wait

seed: ## Write the demonstration herd.
	cd $(BACKEND) && AGRITELEM_DATABASE_URL='$(DB_URL)' go run ./cmd/seed

token: dev-keys ## Print a 24-hour token for the demo gateway (all scopes).
	@cd $(BACKEND) && go run ./cmd/devtoken issue -key ../$(DEV)/jwt-private.pem -sub $(GATEWAY) -location $(LOCATION)

sim: ## Post 21 days of simulated telemetry through the API.
	cd $(BACKEND) && go run ./cmd/gateway-sim -token "$$(go run ./cmd/devtoken issue -key ../$(DEV)/jwt-private.pem -sub $(GATEWAY) -location $(LOCATION))"

down: ## Stop the local stack and delete its data.
	docker compose down -v

ios-project: ## Generate the Xcode project from ios-client/project.yml (needs XcodeGen).
	cd $(IOS) && xcodegen generate

ios-test-kit: ## Run the AgritelemKit package tests on macOS.
	cd $(IOS)/Packages/AgritelemKit && swift test

ios-build: ios-project ## Build the iOS app for the simulator.
	$(XCODEBUILD) build

ios-test: ios-project ## Run the app's unit and UI tests in the simulator.
	$(XCODEBUILD) test
