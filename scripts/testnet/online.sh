#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-testnet-online-00003 \
  --network TESTNET \
  --hyper-sync true \
  --connect-ips=127.0.0.1:19000 \
)
