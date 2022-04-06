package cmd
import (

"fmt"
"github.com/coinbase/rosetta-sdk-go/asserter"
"github.com/coinbase/rosetta-sdk-go/server"
"github.com/coinbase/rosetta-sdk-go/types"
"github.com/deso-protocol/core/lib"
"github.com/deso-protocol/rosetta-deso/deso"
"github.com/deso-protocol/rosetta-deso/services"
"github.com/golang/glog"
"log"
"net/http"
"os"
"os/signal"
"syscall"
"testing"
)

func TestRun(t *testing.T) {
	config := &deso.Config{}
	config.Mode = deso.Offline
	config.Params = &lib.DeSoTestnetParams
	config.Network = &types.NetworkIdentifier{
		Blockchain: "DeSo",
		Network: config.Params.NetworkType.String(),
	}
	config.GenesisBlockIdentifier = &types.BlockIdentifier{
		Hash: config.Params.GenesisBlockHashHex,
	}
	config.DataDirectory = "/tmp/rosetta-offline"
	config.Port = 17006
	config.NodePort = 17000
	config.MinerPublicKeys = []string{"tBCKWCLyYKGa4Lb4buEsMGYePEWcaVAqcunvDVD4zVDcH8NoB5EgPF"}
	config.Regtest = true

	node := deso.NewNode(config)
	go node.Start()

	asserter, err := asserter.NewServer(
		deso.OperationTypes,
		true,
		[]*types.NetworkIdentifier{config.Network},
		nil,
		false,
	)
	if err != nil {
		glog.Fatalf("unable to create new server asserter error: %v", err)
		panic(err)
	}

	router := services.NewBlockchainRouter(config, node, asserter)
	loggedRouter := server.LoggerMiddleware(router)
	corsRouter := server.CorsMiddleware(loggedRouter)
	log.Printf("Listening on port %d\n", config.Port)
	go log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", config.Port), corsRouter))

	shutdownListener := make(chan os.Signal)
	signal.Notify(shutdownListener, syscall.SIGINT, syscall.SIGTERM)
	defer func() {
		node.Stop()
		glog.Info("Shutdown complete")
	}()

	<-shutdownListener
}