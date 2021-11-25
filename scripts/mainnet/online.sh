(cd ../../ && go build -o rosetta-deso -gcflags="all=-N -l" main.go && ./rosetta-deso run \
  --mode ONLINE \
  --port 17005 \
  --data-directory /home/daddy/data_dirs/n0_2 \
)
