#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-regtest-online-2023-04-09-00012 \
  --network TESTNET \
  --miner-public-keys tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF \
  --pos-validator-seed "escape gown wasp foam super claw eager foot opera hold roast cement" \
  --block-producer-seed "0xb78043f5087bbaa079c5e2dadcba547dfacb9964b6d334749256d56c7c0f74fd" \
  --regtest \
)