# Syncs from online.sh
(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port=17005 \
  --node-port=18001 \
  --data-directory /tmp/rosetta-testnet-online-20230407-00009 \
  --hypersync=true \
  --sync-type=hypersync \
  --force-checksum=false \
  --network TESTNET \
)
