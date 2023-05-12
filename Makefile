GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

.PHONY: morphnode tendermint clean

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
	if [ ! -d build/tendermint ]; then mkdir -p build/tendermint; fi
	./build/bin/tendermint init --home build/tendermint

run:
	sh run.sh


clean:
	rm -r build


test:
	go test -v ./...



