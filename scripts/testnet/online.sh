#!/usr/bin/env bash
set -v

(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" -tags=relic main.go && ./rosetta-deso run \
  --glog-v=0 \
  --glog-vmodule="*bitcoin_manager*=0,*balance*=0,*view*=0,*frontend*=0,*peer*=0,*addr*=0,*network*=0,*utils*=0,*connection*=0,*main*=0,*server*=0,*mempool*=0,*miner*=0,*blockchain*=0" \
  --mode ONLINE \
  --node-port=18000 \
  --port 17005 \
  --data-directory /tmp/rosetta-testnet-online-20230407-00000 \
  --network TESTNET \
  --glog-v=2 \
  --hypersync=false \
  --sync-type=blocksync \
)
