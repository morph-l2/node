GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

.PHONY: l2node tendermint clean

l2node:
	if [ ! -d build ]; then mkdir build; fi
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/l2node -v $(LDFLAGS) ./cmd/node

tendermint:
	if [ ! -d build ]; then mkdir build; fi
	env GO111MODULE=on CGO_ENABLED=1 go build -o build/tendermint -v $(LDFLAGS) ./cmd/tendermint

all: l2node tendermint

tm-init:
	if [ ! -d build/tm ]; then mkdir build/tm; fi
	./build/tendermint init --home build/tm

run:
	sh run.sh


clean:
	rm -r build


test:
	go test -v ./...



