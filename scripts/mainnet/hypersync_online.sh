(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --glog-v=2 \
  --mode=ONLINE \
  --port=17006 \
  --node-port=18000 \
  --data-directory=/tmp/rosetta-hypersync-online-mainnet-100039 \
  --hypersync=true \
  --disable-slow-sync=true \
  --max-sync-block-height=1999 \
  --connect-ips=54.165.133.136:17000
)

# Instructions:
# Change --connect-ips to a fully synced node with all your fixes
# Start with --max-sync-block-height=1999 to get started
# Run ./hypersync_online.sh
# Separately, checkout rosetta-cli and rosetta-sdk-go at the same
# level as rosetta-deso and core:
# git clone https://github.com/Coinbase/rosetta-cli.git
# git clone https://github.com/deso-protocol/rosetta-sdk-go.git
#
# Change this one line in rosetta-sdk-go:badger_database.go to use a
# PerformanceMaxTableSize of 6144 << 20. See screenshot I sent you.
#
# Then cd into rosetta-cli and make the change I sent you in my screenshot
# YOU MUST BUILD ROSETTA-CLI AFTER MAKING THE TWO CHANGES ABOVE!
# cd into rosetta-cli and run go build . (this will pull in the changes to
# rosetta-sdk-go so that you don't hit the Badger memtablesize error when
# you run a full sync).
#
# After you have done that, you now have a working rosetta-cli binary
# so you can test:
#
# Loop is:
# - Run ./hypersync_online.sh from a fully synced node that works. Wait
#   for it to finish.
# - Then test it using rosetta-cli command below:
#   ROSETTA_CONFIGURATION_FILE=../rosetta-deso/rosetta-cli-conf/mainnet/hypersync_deso.conf ./rosetta-cli check:data
#
# See TG for discussion on what the remaining issue is.


  #--connect-ips="localhost:17000" \
  #--max-sync-block-height=9999
