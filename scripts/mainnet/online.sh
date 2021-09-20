(cd ../../ && go build -o deso-rosetta -gcflags="all=-N -l" main.go && ./deso-rosetta run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-online-mainnet \
  --txindex \
)
