(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --glog-v=2 \
  --mode=ONLINE \
  --port=17006 \
  --node-port=18000 \
  --data-directory=/tmp/rosetta-hypersync-online-mainnet-100039 \
  --hypersync=true \
  --disable-slow-sync=true \
  --connect-ips=54.165.133.136:17000
)

  #--connect-ips="localhost:17000" \
  #--max-sync-block-height=9999
