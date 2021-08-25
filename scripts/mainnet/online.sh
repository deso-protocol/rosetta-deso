(cd ../../ && go build -o rosetta-bitclout -gcflags="all=-N -l" main.go && ./rosetta-bitclout run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /tmp/rosetta-online-mainnet \
  --txindex \
)
