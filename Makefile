GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

.PHONY: l2node tendermint clean

l2node:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod tidy
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/l2node -v $(LDFLAGS) ./cmd/node

tendermint:
	if [ ! -d build/bin ]; then mkdir -p build/bin; fi
	go mod tidy
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/bin/tendermint -v $(LDFLAGS) ./cmd/tendermint

all: l2node tendermint

tm-init:
	if [ ! -d build/tendermint ]; then mkdir -p build/tendermint; fi
	./build/bin/tendermint init --home build/tendermint

run:
	sh run.sh


clean:
	rm -r build


test:
	go test -v ./...



