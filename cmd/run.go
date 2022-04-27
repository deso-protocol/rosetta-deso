package cmd

import (
	"fmt"
	"log"
	"net/http"
	"os"

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

func init() {
	runCmd.PersistentFlags().String("network", string(deso.Mainnet), "network to connect to")
	runCmd.PersistentFlags().String("mode", string(deso.Online), "mode to start in")
	runCmd.PersistentFlags().Int("port", 17005, "rosetta api listener port")
	runCmd.PersistentFlags().Int("node-port", 17000, "node api listener port")
	runCmd.PersistentFlags().String("data-directory", "/data", "location to store persistent data")
	runCmd.PersistentFlags().StringSlice("miner-public-keys", []string{}, "a list of public keys for testnet mining")
	runCmd.PersistentFlags().Bool("regtest", false, "don't connect to dorsey testnet, mine and spend blocks instantly")
	runCmd.PersistentFlags().StringSlice("connect-ips", []string{}, "list of addresses to only connect to")

	runCmd.PersistentFlags().String("log-dir", "", "The directory for logs")
	runCmd.PersistentFlags().Uint64("glog-v", 0, "The log level. 0 = INFO, 1 = DEBUG, 2 = TRACE. Defaults to zero")
	runCmd.PersistentFlags().String("glog-vmodule", "",
		"The syntax of the argument is a comma-separated list of pattern=N, "+
			"where pattern is a literal file name (minus the \".go\" suffix) or \"glob\" "+
			"pattern and N is a V level. For instance, -vmodule=gopher*=3 sets the V "+
			"level to 3 in all Go files whose names begin \"gopher\".")

	runCmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		viper.BindPFlag(flag.Name, flag)
	})

	rootCmd.AddCommand(runCmd)
}
