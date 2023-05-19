# Build Geth in a stock Go builder container
FROM golang:1.20-alpine as builder

RUN apk add --no-cache gcc musl-dev linux-headers git make

COPY .. /node
RUN cd /node && make all

FROM alpine:latest

RUN apk add --no-cache ca-certificates
COPY --from=builder /node/build/bin/morphnode /usr/local/bin/
COPY --from=builder /node/build/bin/tendermint /usr/local/bin/
COPY --from=builder /node/ops-morphism/entrypoint.sh /entrypoint.sh

VOLUME ["/data"]

ENTRYPOINT ["/bin/sh", "/entrypoint.sh"]