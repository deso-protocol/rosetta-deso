package deso

import (
	"flag"
	"fmt"
	"github.com/deso-protocol/go-deadlock"
	"github.com/dgraph-io/badger/v3"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/btcsuite/btcd/addrmgr"
	"github.com/btcsuite/btcd/wire"
	"github.com/davecgh/go-spew/spew"
	"github.com/deso-protocol/core/lib"
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
		glog.V(3).Infof("_addSeedAddrs: DNS discovery failed on seed host (continuing on): %s %v\n", host, err)
		return
	}
	if len(ipAddrs) == 0 {
		glog.V(3).Infof("_addSeedAddrs: No IPs found for host: %s\n", host)
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
					glog.V(3).Infof("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
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
					glog.V(3).Infof("_addSeedAddrsFromPrefixes: Querying DNS seed: %s", dnsString)
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
	Params       *lib.DeSoParams
	EventManager *lib.EventManager
	Index        *RosettaIndex
	Online       bool
	Config       *Config

	// False when a NewNode is created, set to true on Start(), set to false
	// after Stop() is called. Mainly used in testing.
	isRunning    bool
	runningMutex sync.Mutex

	internalExitChan chan os.Signal
	nodeMessageChan  chan lib.NodeMessage
	stopWaitGroup    sync.WaitGroup
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
	node.runningMutex.Lock()
	defer node.runningMutex.Unlock()

	// Print config
	glog.Infof("Start() | After node glog config")

	node.internalExitChan = make(chan os.Signal)
	node.nodeMessageChan = make(chan lib.NodeMessage)
	go node.listenToRestart()

	if node.Config.Regtest {
		node.Params.EnableRegtest()
	}
	lib.GlobalDeSoParams = *node.Params

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
	node.Index = NewIndex(rosettaIndex, node.chainDB)

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

	var blsKeyStore *lib.BLSKeystore
	if node.Config.PosValidatorSeed != "" {
		blsKeyStore, err = lib.NewBLSKeystore(node.Config.PosValidatorSeed)
		if err != nil {
			panic(err)
		}
	}
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
		node.Config.SyncType,
		node.Config.MaxSyncBlockHeight,
		false,
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
		node.Config.BlockProducerSeed,
		[]string{},
		0,
		node.EventManager,
		node.nodeMessageChan,
		node.Config.ForceChecksum,
		"",
		lib.HypersyncDefaultMaxQueueSize,
		blsKeyStore,
		3000000000, // 3GB max mempool size bytes
		30000,      // 30 seconds mempool back up time millis
		1,          // 1, mempool fee estimator num mempool blocks
		50,         // 50, mempool fee estimator num past blocks
		10,         // 10 milliseconds, augmented block view refresh interval millis
		1500,       // 1500 milliseconds, pos block production interval milliseconds
		30000,      // 30 seconds, pos timeout base duration milliseconds
		10000,      // 10K limit on state sync mempool txn sync
	)
	// Set the snapshot on the rosetta index.
	node.Index.snapshot = node.GetBlockchain().Snapshot()
	if err != nil {
		if shouldRestart {
			glog.Infof(lib.CLog(lib.Red, fmt.Sprintf("Start: Got en error while starting server and shouldRestart "+
				"is true. Node will be erased and resynced. Error: (%v)", err)))
			node.nodeMessageChan <- lib.NodeErase
			return
		}
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

	syscallChannel := make(chan os.Signal)
	signal.Notify(syscallChannel, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case _, open := <-node.internalExitChan:
			if !open {
				return
			}
		case <-syscallChannel:
		}

		node.Stop()
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
	node.runningMutex.Lock()
	defer node.runningMutex.Unlock()

	if !node.isRunning {
		return
	}
	node.isRunning = false
	glog.Infof(lib.CLog(lib.Yellow, "Node is shutting down. This might take a minute. Please don't "+
		"close the node now or else you might corrupt the state."))

	// Server
	glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Stopping server..."))
	node.Server.Stop()
	glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Server successfully stopped."))

	// Snapshot
	snap := node.Server.GetBlockchain().Snapshot()
	if snap != nil {
		glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Stopping snapshot..."))
		snap.Stop()
		node.closeDb(snap.SnapshotDb, "snapshot")
		glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Snapshot successfully stopped."))
	}

	// Databases
	glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Closing all databases..."))
	node.closeDb(node.chainDB, "chain")
	node.closeDb(node.Index.db, "rosetta")
	node.stopWaitGroup.Wait()
	glog.Infof(lib.CLog(lib.Yellow, "Node.Stop: Databases successfully closed."))

	if node.internalExitChan != nil {
		close(node.internalExitChan)
		node.internalExitChan = nil
	}
}

// Close a database and handle the stopWaitGroup accordingly.
func (node *Node) closeDb(db *badger.DB, dbName string) {
	node.stopWaitGroup.Add(1)

	glog.Infof("Node.closeDb: Preparing to close %v db", dbName)
	go func() {
		defer node.stopWaitGroup.Done()
		if err := db.Close(); err != nil {
			glog.Fatalf(lib.CLog(lib.Red, fmt.Sprintf("Node.Stop: Problem closing %v db: err: (%v)", dbName, err)))
		} else {
			glog.Infof(lib.CLog(lib.Yellow, fmt.Sprintf("Node.closeDb: Closed %v Db", dbName)))
		}
	}()
}

func (node *Node) listenToRestart(exitChannels ...*chan os.Signal) {
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
		go node.Start(exitChannels...)
		break
	}
}
