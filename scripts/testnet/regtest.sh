#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-regtest-online-2023-04-09-00012 \
  --network TESTNET \
  --miner-public-keys tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF \
  --regtest \
)
