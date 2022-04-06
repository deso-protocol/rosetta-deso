package deso

import (
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/btcsuite/btcd/addrmgr"
	"github.com/btcsuite/btcd/wire"
	"github.com/davecgh/go-spew/spew"
	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/go-deadlock"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
)

func getAddrsToListenOn(protocolPort int) ([]net.TCPAddr, []net.Listener) {
	listeningAddrs := []net.TCPAddr{}
	listeners := []net.Listener{}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		// TODO: Error handling
	} else {
		for _, iAddr := range ifaceAddrs {
			ifaceIP, _, err := net.ParseCIDR(iAddr.String())
			if err != nil {
				// TODO: Error handling
				continue
			}

			if ifaceIP.IsLinkLocalUnicast() {
				// TODO: Error handling
				continue
			}

			netAddr := net.TCPAddr{
				IP:   ifaceIP,
				Port: int(protocolPort),
			}

			listener, err := net.Listen(netAddr.Network(), netAddr.String())
			if err != nil {
				// TODO: Error handling
				continue
			}

			listeners = append(listeners, listener)
			listeningAddrs = append(listeningAddrs, netAddr)

			glog.Infof("Listening for connections on %s", netAddr.String())
		}
	}

	// TODO: Error handling
	return listeningAddrs, listeners
}

func addIPsForHost(desoAddrMgr *addrmgr.AddrManager, host string, params *lib.DeSoParams) {
	ipAddrs, err := net.LookupIP(host)
	if err != nil {
		glog.V(2).Info("_addSeedAddrs: DNS discovery failed on seed host (continuing on): %s %v\n", host, err)
		return
	}
	if len(ipAddrs) == 0 {
		glog.V(2).Info("_addSeedAddrs: No IPs found for host: %s\n", host)
		return
	}

	// Don't take more than 5 IPs per host.
	ipsPerHost := 5
	if len(ipAddrs) > ipsPerHost {
		glog.V(1).Infof("_addSeedAddrs: Truncating IPs found from %d to %d\n", len(ipAddrs), ipsPerHost)
		ipAddrs = ipAddrs[:ipsPerHost]
	}

	glog.V(1).Infof("_addSeedAddrs: Adding seed IPs from seed %s: %v\n", host, ipAddrs)

	// Convert addresses to NetAddress'es.
	netAddrs := make([]*wire.NetAddress, len(ipAddrs))
	for ii, ip := range ipAddrs {
		netAddrs[ii] = wire.NewNetAddressTimestamp(
			// We initialize addresses with a
			// randomly selected "last seen time" between 3
			// and 7 days ago similar to what bitcoind does.
			time.Now().Add(-1*time.Second*time.Duration(lib.SecondsIn3Days+
				lib.RandInt32(lib.SecondsIn4Days))),
			0,
			ip,
			params.DefaultSocketPort)
	}
	glog.V(1).Infof("_addSeedAddrs: Computed the following wire.NetAddress'es: %s", spew.Sdump(netAddrs))

	// Normally the second argument is the source who told us about the
	// addresses we're adding. In this case since the source is a DNS seed
	// just use the first address in the fetch as the source.
	desoAddrMgr.AddAddresses(netAddrs, netAddrs[0])
}

func addSeedAddrsFromPrefixes(desoAddrMgr *addrmgr.AddrManager, params *lib.DeSoParams) {
	MaxIterations := 99999

	// This one iterates sequentially.
	go func() {
		for dnsNumber := 0; dnsNumber < MaxIterations; dnsNumber++ {
			var wg deadlock.WaitGroup
			for _, dnsGeneratorOuter := range params.DNSSeedGenerators {
				wg.Add(1)
				go func(dnsGenerator []string) {
					dnsString := fmt.Sprintf("%s%d%s", dnsGenerator[0], dnsNumber, dnsGenerator[1])
					glog.V(2).Info("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
					addIPsForHost(desoAddrMgr, dnsString, params)
					wg.Done()
				}(dnsGeneratorOuter)
			}
			wg.Wait()
		}
	}()

	// This one iterates randomly.
	go func() {
		for index := 0; index < MaxIterations; index++ {
			dnsNumber := int(rand.Int63() % int64(MaxIterations))
			var wg deadlock.WaitGroup
			for _, dnsGeneratorOuter := range params.DNSSeedGenerators {
				wg.Add(1)
				go func(dnsGenerator []string) {
					dnsString := fmt.Sprintf("%s%d%s", dnsGenerator[0], dnsNumber, dnsGenerator[1])
					glog.V(2).Info("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
					addIPsForHost(desoAddrMgr, dnsString, params)
					wg.Done()
				}(dnsGeneratorOuter)
			}
			wg.Wait()
		}
	}()
}

type Node struct {
	*lib.Server
	chainDB      *badger.DB
	Snapshot     *lib.Snapshot
	Params       *lib.DeSoParams
	EventManager *lib.EventManager
	Index        *Index
	Online       bool
	Config       *Config

	// False when a NewNode is created, set to true on Start(), set to false
	// after Stop() is called. Mainly used in testing.
	isRunning        bool
	internalExitChan chan os.Signal
	nodeMessageChan  chan lib.NodeMessage
}

func NewNode(config *Config) *Node {
	result := Node{}
	result.Config = config
	result.Online = config.Mode == Online
	result.Params = config.Params
	result.internalExitChan = make(chan os.Signal)
	result.nodeMessageChan = make(chan lib.NodeMessage)

	return &result
}

func (node *Node) Start(exitChannels ...*chan os.Signal) {
	// TODO: Replace glog with logrus so we can also get rid of flag library
	flag.Set("alsologtostderr", "true")
	flag.Set("log_dir", node.Config.LogDirectory)
	flag.Set("v", fmt.Sprintf("%d", node.Config.GlogV))
	flag.Set("vmodule", node.Config.GlogVmodule)
	flag.Parse()
	glog.CopyStandardLogTo("INFO")

	// Print config
	glog.Infof("Start() | After node glog config")

	node.internalExitChan = make(chan os.Signal)
	node.nodeMessageChan = make(chan lib.NodeMessage)
	signal.Notify(node.internalExitChan, syscall.SIGINT, syscall.SIGTERM)

	if node.Config.Regtest {
		node.Params.EnableRegtest()
	}

	desoAddrMgr := addrmgr.New(node.Config.DataDirectory, net.LookupIP)
	desoAddrMgr.Start()

	listeningAddrs, listeners := getAddrsToListenOn(node.Config.NodePort)

	if node.Online && len(node.Config.ConnectIPs) == 0 {
		for _, addr := range listeningAddrs {
			netAddr := wire.NewNetAddress(&addr, 0)
			_ = desoAddrMgr.AddLocalAddress(netAddr, addrmgr.BoundPrio)
		}

		for _, host := range node.Config.Params.DNSSeeds {
			addIPsForHost(desoAddrMgr, host, node.Config.Params)
		}

		go addSeedAddrsFromPrefixes(desoAddrMgr, node.Config.Params)
	}

	var err error
	dbDir := lib.GetBadgerDbPath(node.Config.DataDirectory)
	opts := lib.PerformanceBadgerOptions(dbDir)
	opts.ValueDir = dbDir
	node.chainDB, err = badger.Open(opts)
	if err != nil {
		panic(err)
	}

	// Note: This is one of many seeds. We specify it explicitly for convenience,
	// but not specifying it would make the code run just the same.
	connectIPs := node.Config.ConnectIPs
	if len(connectIPs) == 0 && node.Params.NetworkType == lib.NetworkType_MAINNET {
		connectIPs = append(connectIPs, "deso-seed-4.io")
	}

	// Setup rosetta index
	rosettaIndexDir := filepath.Join(node.Config.DataDirectory, "index")
	rosettaIndexOpts := lib.PerformanceBadgerOptions(rosettaIndexDir)
	rosettaIndexOpts.ValueDir = rosettaIndexDir
	rosettaIndex, err := badger.Open(rosettaIndexOpts)
	node.Index = NewIndex(rosettaIndex)

	// Listen to transaction and block events so we can fill RosettaIndex with relevant data
	node.EventManager = lib.NewEventManager()
	node.EventManager.OnTransactionConnected(node.handleTransactionConnected)
	node.EventManager.OnBlockConnected(node.handleBlockConnected)
	node.EventManager.OnSnapshotCompleted(node.handleSnapshotCompleted)

	minerCount := uint64(1)
	maxBlockTemplatesToCache := uint64(100)
	minBlockUpdateInterval := uint64(10)
	blockCypherAPIKey := ""
	mempoolDumpDir := ""
	disableNetworking := !node.Online
	readOnly := !node.Online
	targetOutboundPeers := uint32(8)
	maxInboundPeers := uint32(125)
	rateLimitFeerateNanosPerKB := uint64(0)
	stallTimeoutSeconds := uint64(900)

	shouldRestart := false
	node.Server, err, shouldRestart = lib.NewServer(
		node.Config.Params,
		listeners,
		desoAddrMgr,
		connectIPs,
		node.chainDB,
		nil,
		targetOutboundPeers,
		maxInboundPeers,
		node.Config.MinerPublicKeys,
		minerCount,
		true,
		node.Config.HyperSync,
		node.Config.DisableSlowSync,
		node.Config.MaxSyncBlockHeight,
		rateLimitFeerateNanosPerKB,
		MinFeeRateNanosPerKB,
		stallTimeoutSeconds,
		maxBlockTemplatesToCache,
		minBlockUpdateInterval,
		blockCypherAPIKey,
		true,
		lib.SnapshotBlockHeightPeriod,
		node.Config.DataDirectory,
		mempoolDumpDir,
		disableNetworking,
		readOnly,
		false,
		nil,
		"",
		[]string{},
		0,
		node.EventManager,
		node.nodeMessageChan,
	)
	if err != nil {
		panic(err)
	}

	if !shouldRestart {
		node.Server.Start()
	}
	node.isRunning = true

	if shouldRestart {
		if node.nodeMessageChan != nil {
			node.nodeMessageChan <- lib.NodeRestart
		}
	}

	go func() {
		<-node.internalExitChan
		node.Stop()
		if node.internalExitChan != nil {
			close(node.internalExitChan)
			node.internalExitChan = nil
		}
		for _, channel := range exitChannels {
			if *channel != nil {
				close(*channel)
				*channel = nil
			}
		}
		glog.Info(lib.CLog(lib.Yellow, "Core node shutdown complete"))
	}()
}

func (node *Node) Stop() {
	if !node.isRunning {
		return
	}
	node.isRunning = false

	glog.Infof(lib.CLog(lib.Yellow, "Node is shutting down. This might take a minute. Please don't "+
		"close the node now or else you might corrupt the state."))

	node.Server.Stop()

	if node.Server.GetBlockchain().Snapshot() != nil {
		node.Server.GetBlockchain().Snapshot().Stop()
	}

	if err := node.chainDB.Close(); err != nil {
		panic(errors.Wrapf(err, "Problem stopping blockchain db"))
	}
	if err := node.Index.db.Close(); err != nil {
		panic(errors.Wrapf(err, "Problem stopping index db"))
	}

	if node.internalExitChan != nil {
		close(node.internalExitChan)
		node.internalExitChan = nil
	}
}

func (node *Node) listenToRestart() {
	select {
	case <-node.internalExitChan:
		break
	case operation := <-node.nodeMessageChan:
		if !node.isRunning {
			panic("Node.listenToRestart: Node is currently not running, nodeMessageChan should've not been called!")
		}
		glog.Infof("Node.listenToRestart: Stopping node")
		node.Stop()
		glog.Infof("Node.listenToRestart: Finished stopping node")
		switch operation {
		case lib.NodeErase:
			if err := os.RemoveAll(node.Config.DataDirectory); err != nil {
				glog.Fatal(lib.CLog(lib.Red, fmt.Sprintf("IMPORTANT: Problem removing the directory (%v), you "+
					"should run `rm -rf %v` to delete it manually. Error: (%v)", node.Config.DataDirectory,
					node.Config.DataDirectory, err)))
				return
			}
		}

		glog.Infof("Node.listenToRestart: Restarting node")
		go node.Start()
		break
	}
}
