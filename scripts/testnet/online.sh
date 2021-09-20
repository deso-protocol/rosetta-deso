#!/usr/bin/env bash
set -v

(cd ../../ && go build -o deso-rosetta -gcflags="all=-N -l" main.go && ./deso-rosetta run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-online \
  --network TESTNET \
  --miner-public-keys tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF \
  --regtest \
)
