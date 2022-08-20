# Syncs from online.sh
(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port=17006 \
  --node-port=18001 \
  --data-directory /tmp/rosetta-testnet-online-xadldldlxlsdk00002 \
  --hypersync=true \
  --sync-type=hypersync \
  --network TESTNET
)
