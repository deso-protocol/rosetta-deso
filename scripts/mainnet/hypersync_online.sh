(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --glog-v=0 \
  --mode=ONLINE \
  --port=17006 \
  --node-port=18000 \
  --data-directory=/tmp/rosetta-hypersync-online-mainnet-100067 \
  --hypersync=true \
  --max-sync-block-height=17000 \
  --disable-slow-sync=false \
  --connect-ips=deso-seed-2.io:17000
)