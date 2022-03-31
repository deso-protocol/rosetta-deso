(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode=ONLINE \
  --node-port=17000 \
  --port=17005 \
  --data-directory=/tmp/rosetta-online-mainnet-00025 \
  --hypersync=true \
  --disable-slow-sync=false \
  --max-sync-block-height=9999
)

