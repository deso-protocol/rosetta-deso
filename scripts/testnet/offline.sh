#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode OFFLINE \
  --port 17006 \
  --data-directory /tmp/rosetta-offline-00000 \
  --network TESTNET \
  --regtest \
)
