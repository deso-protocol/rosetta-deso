#!/usr/bin/env bash

(cd ../../ && go build -o rosetta-bitclout -gcflags="all=-N -l" main.go && ./rosetta-bitclout run \
  --mode OFFLINE \
  --port 17006 \
  --data-directory /tmp/rosetta-offline \
  --network TESTNET \
)
