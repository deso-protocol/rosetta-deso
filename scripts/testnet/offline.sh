#!/usr/bin/env bash
set -v

(cd ../../ && go build -o deso-rosetta -gcflags="all=-N -l" main.go && ./deso-rosetta run \
  --mode OFFLINE \
  --port 17006 \
  --data-directory /tmp/rosetta-offline \
  --network TESTNET \
  --regtest \
)
