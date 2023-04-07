#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --node-port=18000 \
  --port 17005 \
  --data-directory /tmp/rosetta-testnet-online-20230407-00000 \
  --network TESTNET \
  --connect-ips=test.deso.org:18000 \
  --glog-v=2 \
  --hypersync=true \
  --sync-type=hypersync \
)
