module github.com/deso-protocol/rosetta-deso

go 1.16

replace github.com/deso-protocol/core => ../core/

require (
	github.com/btcsuite/btcd v0.21.0-beta
	github.com/coinbase/rosetta-sdk-go v0.6.10
	github.com/davecgh/go-spew v1.1.1
	github.com/deso-protocol/core v1.1.0
	github.com/deso-protocol/go-deadlock v1.0.0
	github.com/deso-protocol/go-merkle-tree v1.0.0
	github.com/dgraph-io/badger/v3 v3.2103.0
	github.com/golang/glog v1.0.0
	github.com/laser/go-merkle-tree v0.0.0-20180821204614-16c2f6ea4444
	github.com/mitchellh/go-homedir v1.1.0
	github.com/mmcloughlin/avo v0.0.0-20201105074841-5d2f697d268f // indirect
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
)
