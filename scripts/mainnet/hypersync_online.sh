(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode=ONLINE \
  --port=17006 \
  --node-port=18000 \
  --connect-ips=localhost:17000 \
  --data-directory=/tmp/rosetta-online-mainnet-10010 \
  --hypersync=true \
  --disable-slow-sync=true \
  --max-sync-block-height=9999
)
