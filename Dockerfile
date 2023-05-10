# Build Geth in a stock Go builder container
FROM golang:1.20-alpine as builder

RUN apk add --no-cache gcc musl-dev linux-headers git make

ADD . /l2-node
RUN cd /l2-node && make all

FROM alpine:latest

RUN apk add --no-cache ca-certificates
COPY --from=builder /l2-node/build/bin/l2node /usr/local/bin/
COPY --from=builder /l2-node/build/bin/tendermint /usr/local/bin/
COPY entrypoint.sh /entrypoint.sh

VOLUME ["/data"]

ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]