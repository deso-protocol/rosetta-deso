#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --node-port=18000 \
  --port 17005 \
  --data-directory /tmp/rosetta-testnet-online-00000 \
  --network TESTNET \
)
