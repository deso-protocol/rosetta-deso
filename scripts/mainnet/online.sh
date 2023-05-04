(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode=ONLINE \
  --node-port=17000 \
  --port=17005 \
  --data-directory=/tmp/rosetta-online-mainnet-00025 \
  --sync-type=blocksync \
  --hypersync=false \
)
