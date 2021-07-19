#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-bitclout -gcflags="all=-N -l" main.go && ./rosetta-bitclout run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-online \
  --network TESTNET \
  --miner-public-keys tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF \
)
