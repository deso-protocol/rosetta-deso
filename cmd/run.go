package cmd

import (
	"fmt"
	"log"
	"net/http"
	"os"

	coreCmd "github.com/deso-protocol/core/cmd"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/deso-protocol/rosetta-deso/deso"
	"github.com/deso-protocol/rosetta-deso/services"
)

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := deso.LoadConfig()
		if err != nil {
			return err
		}

		shutdownListener := make(chan os.Signal)
		node := deso.NewNode(config)
		go node.Start(&shutdownListener)

		asserter, err := asserter.NewServer(
			deso.OperationTypes,
			true,
			[]*types.NetworkIdentifier{config.Network},
			nil,
			false,
		)
		if err != nil {
			glog.Fatalf("unable to create new server asserter", "error", err)
			return err
		}

		router := services.NewBlockchainRouter(config, node, asserter)
		loggedRouter := server.LoggerMiddleware(router)
		corsRouter := server.CorsMiddleware(loggedRouter)
		log.Printf("Listening on port %d\n", config.Port)
		go func() {
			glog.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), corsRouter))
		}()

		<-shutdownListener
		return nil
	},
}

// debugCmd represents the debug command. It can help you debug the chain by
// printing txns directly from the badgerdb, among other things. You must modify
// the go code directly to suit your needs. It's not an "out of the box" thing.
var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := deso.LoadConfig()
		if err != nil {
			return err
		}
		// You need to set this or else the block cutovers will mess up your GetBlockIndex in
		// DeserializeBlockNode.
		// Check for regtest mode
		if config.Regtest {
			config.Params.EnableRegtest(config.RegtestAccelerated)
		}
		lib.GlobalDeSoParams = *config.Params

		dbDir := lib.GetBadgerDbPath(config.DataDirectory)
		opts := lib.PerformanceBadgerOptions(dbDir)
		opts.ValueDir = dbDir
		chainDB, err := badger.Open(opts)
		if err != nil {
			panic(err)
		}

		// See if we have a best chain hash stored in the db.
		bestBlockHash := lib.DbGetBestHash(chainDB, nil, lib.ChainTypeDeSoBlock)

		// If there is no best chain hash in the db then it means we've never
		// initialized anything so take the time to do it now.
		if bestBlockHash == nil {
			panic("bestBlockHash is nil")
		}

		blockIndexByHash, err := lib.GetBlockIndex(chainDB, false /*bitcoinNodes*/, config.Params)
		if err != nil {
			panic(fmt.Sprintf("Problem reading block index from db: %v", err))
		}

		tipNode := blockIndexByHash[*bestBlockHash]
		if tipNode == nil {
			panic(fmt.Sprintf("Best hash (%#v) not found in block index", bestBlockHash.String()))
		}
		bestChain, err := lib.GetBestChain(tipNode, blockIndexByHash)
		if err != nil {
			panic(fmt.Sprintf("Problem reading best chain from db: %v", err))
		}

		for _, bestChainNode := range bestChain {
			block, err := lib.GetBlock(bestChainNode.Hash, chainDB, nil)
			if err != nil {
				panic(fmt.Sprintf("Problem reading block from db: %v", err))
			}
			if len(block.Txns) > 1 {
				fmt.Println(block.Header.Height, bestChainNode.Hash.String(), len(block.Txns))
				for _, txn := range block.Txns {
					fmt.Println("\t", txn.Hash().String())
					pubkeyString := "block reward"
					if len(txn.PublicKey) != 0 {
						pubkeyString = lib.PkToStringTestnet(txn.PublicKey)
					}
					fmt.Println("\t\tSender: \t", pubkeyString)
					for _, output := range txn.TxOutputs {
						fmt.Println("\t\tRecipient: \t", lib.PkToStringTestnet(output.PublicKey))
						fmt.Println("\t\tAmount: \t", output.AmountNanos)
					}
				}
			}
		}
		return nil
	},
}

func initFlags(cmdIter *cobra.Command) {
	coreCmd.SetupRunFlags(cmdIter)

	cmdIter.PersistentFlags().String("network", string(deso.Mainnet), "network to connect to")
	cmdIter.PersistentFlags().String("mode", string(deso.Online), "mode to start in")
	cmdIter.PersistentFlags().Int("port", 17005, "rosetta api listener port")
	cmdIter.PersistentFlags().Int("node-port", 17000, "node api listener port")
	cmdIter.PersistentFlags().String("data-directory", "/data", "location to store persistent data")

	cmdIter.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		viper.BindPFlag(flag.Name, flag)
	})

	rootCmd.AddCommand(cmdIter)
}

func init() {
	// This is a dirty hack. When we run in debug mode, we need to set the flags on the debugCmd rather
	// than the runCmd. There is probably a "right" way to do this, but the below works for now...
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		fmt.Println("DEBUGGING!")
		initFlags(debugCmd)
	} else {
		initFlags(runCmd)
	}
}
