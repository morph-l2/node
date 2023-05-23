GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

.PHONY: all morphnode tendermint tm-init clean

morphnode:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod download
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/morphnode -v $(LDFLAGS) ./cmd/node

tendermint:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod download
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/tendermint -v $(LDFLAGS) ./cmd/tendermint

all: morphnode tendermint

tm-init:
	if [ ! -d build ]; then mkdir -p build; fi
	./build/bin/tendermint init --home build

run:
	cd ops-morphism && sh run.sh

clean:
	rm -r build

test:
	go test -v ./...

dev-up:
	cd ops-morphism && docker-compose up -d sequencer_node
.PHONY: dev-up

dev-down:
	cd ops-morphism && docker-compose down
.PHONY: dev-down

dev-clean:
	cd ops-morphism && docker-compose down
	docker image ls '*morphism*' --format='{{.Repository}}' | xargs -r docker rmi
	docker volume ls --filter name=ops-morphism* --format='{{.Name}}' | xargs -r docker volume rm
.PHONY: devnet-clean







