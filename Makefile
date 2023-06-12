GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

morphnode:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod download
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/morphnode -v $(LDFLAGS) ./cmd/node
.PHONY: morphnode

tendermint:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod download
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/tendermint -v $(LDFLAGS) ./cmd/tendermint
.PHONY: tendermint

build: morphnode tendermint
.PHONY: build

init:
	if [ -d build/config ]; then exit 0; fi
	if [ ! -d build ]; then mkdir -p build; fi
	./build/bin/tendermint init --home build
.PHONY: init

run: build init
	cd ops-morphism && sh run.sh

clean:
	rm -r build

test:
	go test -v ./...

devnet-up:
	cd ops-morphism && docker compose up -d
	#cd ops-morphism && docker compose up -d l1 && docker compose up -d sequencer_node
.PHONY: dev-up

devnet-down:
	cd ops-morphism && docker compose down
.PHONY: dev-down

devnet-clean:
	cd ops-morphism && docker compose down
	docker image ls '*morphism*' --format='{{.Repository}}' | xargs -r docker rmi
	docker volume ls --filter name=ops-morphism* --format='{{.Name}}' | xargs -r docker volume rm
.PHONY: devnet-clean

devnet-reset:
	cd ops-morphism && docker compose down
	docker volume ls --filter name=ops-morphism* --format='{{.Name}}' | xargs -r docker volume rm
.PHONY: devnet-reset

testnet-up: build
	sh ./ops-morphism/testnet/tendermint-setup.sh
	cd ops-morphism/testnet && docker compose up -d
	sh ./ops-morphism/testnet/launch.sh
.PHONY: testnet-up

testnet-down:
	PIDS=$$(ps -ef | grep morphnode | grep -v grep | awk '{print $$2}'); \
	if [ -n "$$PIDS" ]; then \
		echo "Processes found: $$PIDS"; \
		kill $$PIDS; \
	else \
		echo "No processes found"; \
	fi
	cd ops-morphism/testnet && docker compose down
.PHONY: testnet-down

testnet-clean: testnet-down
	docker volume ls --filter "name=morph_data*" -q | xargs -r docker volume rm
	rm -rf ./mytestnet
.PHONY: testnet-clean







