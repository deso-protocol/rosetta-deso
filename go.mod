module github.com/bitclout/rosetta-bitclout

go 1.16

replace github.com/golang/glog => ../core/third_party/github.com/golang/glog

replace github.com/laser/go-merkle-tree => ../core/third_party/github.com/laser/go-merkle-tree

replace github.com/sasha-s/go-deadlock => ../core/third_party/github.com/sasha-s/go-deadlock

replace github.com/bitclout/core => ../core/

require (
	github.com/bitclout/core v1.0.7
	github.com/btcsuite/btcd v0.21.0-beta
	github.com/coinbase/rosetta-sdk-go v0.6.10
	github.com/davecgh/go-spew v1.1.1
	github.com/dgraph-io/badger/v3 v3.2011.1
	github.com/golang/glog v0.0.0-20210429001901-424d2337a529
	github.com/laser/go-merkle-tree v0.0.0-20180821204614-16c2f6ea4444
	github.com/mitchellh/go-homedir v1.1.0
	github.com/sasha-s/go-deadlock v0.2.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
)
