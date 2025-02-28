(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode=OFFLINE \
  --port=17006 \
  --data-directory=/tmp/rosetta-offline-mainnet-00025 \
  --sync-type=blocksync \
  --hypersync=false \
)
